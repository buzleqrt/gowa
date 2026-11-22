package ytmusic

import (
	"strconv"
	"strings"
)

// parseTrack membaca satu musicResponsiveListItemRenderer. Hanya baris yang
// punya videoId (lagu/video) yang dianggap hasil yang bisa diputar.
func parseTrack(renderer map[string]any) *Track {
	videoID := findString(renderer, "watchEndpoint", "videoId")
	if videoID == "" {
		return nil
	}

	columns := flexColumns(renderer)
	var texts []string
	for _, col := range columns {
		texts = append(texts, columnText(col))
	}

	title := ""
	if len(texts) > 0 {
		title = strings.TrimSpace(texts[0])
	}

	artist := ""
	if len(texts) > 1 {
		artist = parseArtist(texts[1])
	}

	var duration string
	var plays string
	walk(renderer, func(m map[string]any) {
		if duration != "" {
			return
		}
		if text, ok := m["text"].(string); ok && durationPattern.MatchString(text) {
			duration = text
		}
	})
	for _, t := range texts {
		parts := strings.Split(t, " • ")
		for _, part := range parts {
			if strings.HasSuffix(strings.ToLower(part), "plays") {
				plays = part
			}
			if duration == "" && durationPattern.MatchString(part) {
				duration = part
			}
		}
	}

	return &Track{
		VideoID:           videoID,
		Title:             title,
		Artist:            artist,
		Duration:          parseDuration(duration),
		FormattedDuration: duration,
		Plays:             plays,
		Thumbnail:         lastThumbnail(renderer),
		URL:               musicBase + "/watch?v=" + videoID,
	}
}

func flexColumns(renderer map[string]any) []map[string]any {
	rawColumns, _ := renderer["flexColumns"].([]any)
	columns := make([]map[string]any, 0, len(rawColumns))
	for _, raw := range rawColumns {
		col, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := col["musicResponsiveListItemFlexColumnRenderer"].(map[string]any)
		if !ok {
			continue
		}
		columns = append(columns, inner)
	}
	return columns
}

func columnText(column map[string]any) string {
	text, _ := column["text"].(map[string]any)
	runs, _ := text["runs"].([]any)

	var sb strings.Builder
	for _, raw := range runs {
		run, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := run["text"].(string); ok {
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// parseArtist mengambil nama artis dari subjudul seperti
// "Song • Weird Genius & Sara Fajira" atau "Song • Artist • Album".
func parseArtist(subtitle string) string {
	parts := strings.Split(subtitle, " • ")
	if len(parts) <= 1 {
		return strings.TrimSpace(subtitle)
	}
	rest := parts[1:]
	// "Song • Artist" -> Artist; "Song • Artist • Album" -> Artist.
	return strings.TrimSpace(rest[0])
}

func lastThumbnail(value any) string {
	var url string
	walk(value, func(m map[string]any) {
		renderer, ok := m["musicThumbnailRenderer"].(map[string]any)
		if !ok {
			return
		}
		thumb, ok := renderer["thumbnail"].(map[string]any)
		if !ok {
			return
		}
		list, ok := thumb["thumbnails"].([]any)
		if !ok || len(list) == 0 {
			return
		}
		last, ok := list[len(list)-1].(map[string]any)
		if !ok {
			return
		}
		if s, ok := last["url"].(string); ok {
			url = s
		}
	})
	return url
}

func parseDuration(text string) int {
	if text == "" {
		return 0
	}
	parts := strings.Split(text, ":")
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

func atoi(text string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(text))
	return n
}

func walk(value any, fn func(map[string]any)) {
	switch node := value.(type) {
	case map[string]any:
		fn(node)
		for _, child := range node {
			walk(child, fn)
		}
	case []any:
		for _, child := range node {
			walk(child, fn)
		}
	}
}

func collect(value any, key string) []map[string]any {
	var out []map[string]any
	walk(value, func(m map[string]any) {
		if inner, ok := m[key].(map[string]any); ok {
			out = append(out, inner)
		}
	})
	return out
}

func findString(value any, path ...string) string {
	var found string
	walk(value, func(m map[string]any) {
		if found != "" {
			return
		}
		var node any = m
		for _, key := range path {
			asMap, ok := node.(map[string]any)
			if !ok {
				return
			}
			node = asMap[key]
		}
		if s, ok := node.(string); ok {
			found = s
		}
	})
	return found
}

type apiFormat struct {
	Itag           int        `json:"itag"`
	MimeType       string     `json:"mimeType"`
	Bitrate        int        `json:"bitrate"`
	AverageBitrate int        `json:"averageBitrate"`
	AudioQuality   string     `json:"audioQuality"`
	URL            string     `json:"url"`
	ContentLength  string     `json:"contentLength"`
	AudioTrack     audioTrack `json:"audioTrack"`
}

type audioTrack struct {
	DisplayName    string `json:"displayName"`
	ID             string `json:"id"`
	AudioIsDefault bool   `json:"audioIsDefault"`
}

type playerResponse struct {
	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	VideoDetails struct {
		Title         string `json:"title"`
		Author        string `json:"author"`
		LengthSeconds string `json:"lengthSeconds"`
	} `json:"videoDetails"`
	StreamingData struct {
		AdaptiveFormats []apiFormat `json:"adaptiveFormats"`
	} `json:"streamingData"`
}
