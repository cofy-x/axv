package githubreview

import (
	"context"
	"strings"
)

const unrecognizedReviewScenario = "unrecognized"

type reviewScenarioOffer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Title string `json:"title"`
	Side  string `json:"side"`
}

func (s *store) reviewScenarioOffers(ctx context.Context, started Job) ([]reviewScenarioOffer, error) {
	if started.Evidence == nil || !started.Evidence.Policy.AutoCandidateValidation {
		return nil, nil
	}
	policy := started.Evidence.Policy
	byRepo, err := queryApprovedComparisonScenarios(ctx, s.db, approvedScenariosSelect+" AND s.repository=? ORDER BY s.rowid LIMIT 8", started.account, started.Repository)
	if err != nil {
		return nil, err
	}
	var offers []reviewScenarioOffer
	for _, entry := range byRepo[strings.ToLower(started.Repository)] {
		if !candidateIDPattern.MatchString(entry.ID) || !validReviewPath(entry.Path) || len(entry.Path) > 1024 ||
			!scenarioRuntimeMatches(policy, entry.source.Runtime) || !validComparisonAssertion(entry.source.Assertion) || !verifiedScenarioDifferential(entry.source) {
			continue
		}
		offers = append(offers, reviewScenarioOffer{ID: entry.ID, Name: entry.Name, Path: entry.Path, Title: entry.Title, Side: entry.Side})
	}
	return offers, nil
}

func reviewScenarioOffersForDiff(offers []reviewScenarioOffer, anchors reviewAnchors) []reviewScenarioOffer {
	var relevant []reviewScenarioOffer
	for _, offer := range offers {
		paths := anchors.head
		if offer.Side == "base" {
			paths = anchors.base
		}
		if len(paths[offer.Path]) != 0 {
			relevant = append(relevant, offer)
		}
	}
	return relevant
}

func bindReviewScenarioReferences(findings []Finding, offers []reviewScenarioOffer) {
	offered := make(map[string]bool, len(offers))
	for _, s := range offers {
		offered[s.ID] = true
	}
	for i := range findings {
		if id := findings[i].VerificationScenarioID; id != "" && !offered[id] {
			// Preserve the distinction from omission without retaining model-supplied
			// strings or allowing title fallback after a rejected reference.
			findings[i].VerificationScenarioID = unrecognizedReviewScenario
		}
	}
}
