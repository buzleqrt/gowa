package instagram

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

const (
	userPostsDocID        = "29597370573235791"
	userPostsFriendlyName = "PolarisProfilePostsTabContentQuery_connection"
)

func FetchUserPosts(ctx context.Context, username string, limit int) (*SearchResult, error) {
	username = ExtractUsername(username)
	if username == "" {
		return nil, ErrUserNotFound
	}
	limit = clampLimit(limit)
	if currentSessionID() == "" {
		return nil, ErrSearchLoginRequired
	}

	pageURL := "https://www.instagram.com/" + username + "/"
	session, err := loadWebSession(ctx, pageURL)
	if err != nil {
		return nil, err
	}

	variables := map[string]any{
		"after":  nil,
		"before": nil,
		"data": map[string]any{
			"count":                             12,
			"include_reel_media_seen_timestamp": true,
			"include_relationship_info":         true,
			"latest_besties_reel_media":         true,
			"latest_reel_media":                 true,
		},
		"first":                  12,
		"include_multi_captions": false,
		"last":                   nil,
		"username":               username,
	}

	root, err := session.graphql(ctx, pageURL, userPostsDocID, userPostsFriendlyName, variables)
	if err != nil {
		return nil, err
	}

	posts := collectSearchPosts(root, limit)
	if len(posts) == 0 {
		return nil, ErrTagNotFound
	}

	return &SearchResult{
		Query:   username,
		Tag:     username,
		Account: username,
		Posts:   posts,
	}, nil
}

func ResolveUsername(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("kata kunci pencarian tidak valid")
	}
	if username := ExtractUsername(query); username == query && !strings.Contains(query, " ") {
		return username, nil
	}
	if currentSessionID() == "" {
		return "", ErrSearchLoginRequired
	}

	session, err := loadWebSession(ctx, homeEP)
	if err != nil {
		return "", err
	}

	root, err := session.keywordSearch(ctx, homeEP, query)
	if err != nil {
		return "", err
	}

	candidates := collectUserCandidates(root)
	if len(candidates) == 0 {
		return "", ErrTagNotFound
	}

	queryNorm := normalizeName(query)
	var best string
	var bestScore int
	for _, candidate := range candidates {
		score := scoreUserCandidate(candidate, queryNorm)
		if score > bestScore {
			bestScore = score
			best = candidate.Username
		}
	}
	if best == "" || bestScore < 40 {
		return "", ErrTagNotFound
	}
	return best, nil
}

type userCandidate struct {
	Username string
	FullName string
}

func collectUserCandidates(root map[string]any) []userCandidate {
	var candidates []userCandidate
	seen := make(map[string]bool)

	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			username := stringValue(value["username"])
			fullName := stringValue(value["full_name"])
			if username != "" && !seen[username] && (fullName != "" || stringValue(value["pk"]) != "") {

				if _, hasMedia := value["image_versions2"]; !hasMedia {
					seen[username] = true
					candidates = append(candidates, userCandidate{
						Username: username,
						FullName: fullName,
					})
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(root)
	return candidates
}

func scoreUserCandidate(candidate userCandidate, queryNorm string) int {
	userNorm := normalizeName(candidate.Username)
	fullNorm := normalizeName(candidate.FullName)

	score := 0
	switch {
	case fullNorm != "" && fullNorm == queryNorm:
		score = 100
	case userNorm == queryNorm:
		score = 95
	case fullNorm != "" && (strings.Contains(fullNorm, queryNorm) || strings.Contains(queryNorm, fullNorm)):
		score = 80
	case strings.Contains(userNorm, queryNorm) || strings.Contains(queryNorm, userNorm):
		score = 70
	default:
		for _, token := range strings.Fields(strings.ToLower(candidate.FullName + " " + candidate.Username)) {
			if strings.Contains(queryNorm, normalizeName(token)) && normalizeName(token) != "" {
				score += 20
			}
		}
	}
	return score
}

func normalizeName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
