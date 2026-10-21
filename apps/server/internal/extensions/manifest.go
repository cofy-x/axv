// Package extensions owns explicitly installed, low-privilege HTML applications.
package extensions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxPackageBytes = 1 << 20
	MaxHTMLBytes    = 512 << 10
	MaxDataBytes    = 128 << 10
)

var appID = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
var versionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)

// Icon names select host-owned artwork; packages never provide markup or URLs.
type Icon string

const (
	IconDocument  Icon = "document"
	IconChecklist Icon = "checklist"
)

func (icon *Icon) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if name != string(IconDocument) && name != string(IconChecklist) {
		return failure("invalid_extension_package", "Select a supported application icon: document or checklist.")
	}
	*icon = Icon(name)
	return nil
}

type Manifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Version       string   `json:"version"`
	APIVersion    int      `json:"apiVersion"`
	Capabilities  []string `json:"capabilities"`
	Icon          Icon     `json:"icon,omitempty"`
}

type Package struct {
	Manifest Manifest `json:"manifest"`
	HTML     string   `json:"html"`
}

type Record struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Version      string   `json:"version"`
	APIVersion   int      `json:"apiVersion"`
	Capabilities []string `json:"capabilities"`
	Digest       string   `json:"digest"`
	Generation   int64    `json:"generation"`
	Enabled      bool     `json:"enabled"`
	Installed    bool     `json:"installed"`
	Icon         Icon     `json:"icon,omitempty"`
}

type Document struct {
	Value    json.RawMessage `json:"value"`
	Revision int64           `json:"revision"`
}

type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{Code: code, Message: message} }

func validatePackage(p Package) (canonical []byte, digest string, err error) {
	if err := validateManifest(p.Manifest); err != nil {
		return nil, "", err
	}
	if len(p.HTML) == 0 || len(p.HTML) > MaxHTMLBytes || !utf8.ValidString(p.HTML) || strings.ContainsRune(p.HTML, 0) {
		return nil, "", failure("invalid_extension_package", "Provide a UTF-8 HTML document of at most 512 KiB.")
	}
	canonical, err = json.Marshal(p)
	if err != nil || len(canonical) > MaxPackageBytes {
		return nil, "", failure("invalid_extension_package", "The extension package exceeds 1 MiB.")
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func validateManifest(m Manifest) error {
	if m.SchemaVersion != 1 || m.APIVersion != 1 {
		return failure("invalid_extension_package", "This extension uses an unsupported manifest or API version.")
	}
	if len(m.ID) > 96 || !appID.MatchString(m.ID) {
		return failure("invalid_extension_package", "Use a lowercase, namespaced extension ID such as local.checklist.")
	}
	if strings.TrimSpace(m.Title) != m.Title || m.Title == "" || utf8.RuneCountInString(m.Title) > 80 || strings.IndexFunc(m.Title, unicode.IsControl) >= 0 || !versionName.MatchString(m.Version) {
		return failure("invalid_extension_package", "Provide a title of at most 80 characters and a valid version of at most 64 characters.")
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != "storage" {
		return failure("invalid_extension_package", "This extension API supports only the storage capability.")
	}
	if m.Icon != "" && m.Icon != IconDocument && m.Icon != IconChecklist {
		return failure("invalid_extension_package", "Select a supported application icon: document or checklist.")
	}
	return nil
}

// decodeJSON rejects ambiguous duplicate keys as well as unknown fields and
// trailing JSON values, so the reviewed package has exactly one interpretation.
func decodeJSON(data []byte, into any) error {
	probe := json.NewDecoder(bytes.NewReader(data))
	probe.UseNumber()
	if err := uniqueJSON(probe); err != nil {
		return err
	}
	if _, err := probe.Token(); err != io.EOF {
		return fmt.Errorf("provide exactly one JSON value")
	}
	if err := exactFields(data, reflect.TypeOf(into)); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("provide exactly one JSON value")
	}
	return nil
}

// encoding/json accepts case-insensitive struct fields. The extension protocol
// deliberately accepts only its exact documented spelling, including when two
// differently cased keys would otherwise overwrite the same field.
func exactFields(data []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(json.RawMessage(nil)) || typ.Kind() != reflect.Struct {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if f.IsExported() && name != "" && name != "-" {
			allowed[name] = f.Type
		}
	}
	for key, value := range fields {
		fieldType, ok := allowed[key]
		if !ok {
			return fmt.Errorf("unknown JSON field %q", key)
		}
		if err := exactFields(value, fieldType); err != nil {
			return err
		}
	}
	return nil
}

func uniqueJSON(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate or invalid JSON field")
			}
			seen[s] = true
			if err := uniqueJSON(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid JSON value")
	}
	_, err = d.Token()
	return err
}
