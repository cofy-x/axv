package access

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func networkAddress(name, value string) (protocol.NetworkAddress, bool) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return protocol.NetworkAddress{}, false
	}
	address := prefix.Addr().Unmap()
	if !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return protocol.NetworkAddress{}, false
	}
	family, bits := "ipv6", prefix.Bits()
	if address.Is4() {
		family = "ipv4"
		if prefix.Addr().Is4In6() {
			bits -= 96
		}
	}
	return protocol.NetworkAddress{Interface: name, Address: address.String(), PrefixLength: bits, Family: family,
		Reachability: protocol.Reachability{From: "deployment-host", State: "unknown", Reason: "not_verified"}}, true
}

func (s *Server) network(w http.ResponseWriter, _ *http.Request) {
	info := protocol.NetworkInfo{Version: protocol.Version, MachineID: s.machineID, BootID: s.bootID,
		ObservedAt: time.Now().UTC(), Addresses: []protocol.NetworkAddress{}}
	interfaces, err := net.Interfaces()
	if err != nil {
		problem(w, 503, "network_unavailable", "Guest network interfaces could not be observed.")
		return
	}
	for _, network := range interfaces {
		if network.Flags&net.FlagUp == 0 || network.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := network.Addrs()
		if err != nil {
			problem(w, 503, "network_unavailable", "Guest network addresses could not be observed.")
			return
		}
		for _, value := range addresses {
			if address, ok := networkAddress(network.Name, value.String()); ok {
				info.Addresses = append(info.Addresses, address)
				if len(info.Addresses) > 256 {
					problem(w, 503, "network_unavailable", "Guest network observation exceeds its limit.")
					return
				}
			}
		}
	}
	sort.Slice(info.Addresses, func(i, j int) bool {
		if info.Addresses[i].Interface != info.Addresses[j].Interface {
			return info.Addresses[i].Interface < info.Addresses[j].Interface
		}
		return info.Addresses[i].Address < info.Addresses[j].Address
	})
	reply(w, http.StatusOK, info)
}
