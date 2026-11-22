package pinterest

import (
	"encoding/json"
	"strings"
)

// flexText menerima teks Pinterest yang kadang berupa string biasa dan
// kadang berupa objek {"text": ...} (misalnya judul hasil pencarian).
type flexText string

func (t *flexText) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*t = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = flexText(s)
		return nil
	}

	var obj struct {
		Text   string `json:"text"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*t = flexText(firstNonEmpty(obj.Text, obj.Format))
	return nil
}

func (t flexText) String() string {
	return string(t)
}

type frameImage struct {
	URL string `json:"url"`
}

type frameImages struct {
	Orig  frameImage `json:"orig"`
	Large frameImage `json:"736x"`
	Mid   frameImage `json:"564x"`
	Small frameImage `json:"236x"`
}

func (fi frameImages) best() string {
	switch {
	case fi.Orig.URL != "":
		return fi.Orig.URL
	case fi.Large.URL != "":
		return fi.Large.URL
	case fi.Mid.URL != "":
		return fi.Mid.URL
	}
	return fi.Small.URL
}

type videoVariant struct {
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int64  `json:"duration"`
}

type videosData struct {
	VideoList map[string]videoVariant `json:"video_list"`
	Duration  int64                   `json:"duration"`
}

type carouselSlot struct {
	Title   flexText    `json:"title"`
	Details flexText    `json:"details"`
	Images  frameImages `json:"images"`
}

type carouselData struct {
	Slots []carouselSlot `json:"carousel_slots"`
}

type storyBlock struct {
	Image *struct {
		Images struct {
			Originals frameImage `json:"originals"`
			Large     frameImage `json:"736x"`
		} `json:"images"`
	} `json:"image"`
}

type storyPage struct {
	Title  flexText     `json:"title"`
	Blocks []storyBlock `json:"blocks"`
}

type storyData struct {
	Pages []storyPage `json:"pages"`
}

type framePin struct {
	ID            string        `json:"id"`
	Title         flexText      `json:"title"`
	GridTitle     flexText      `json:"grid_title"`
	Description   flexText      `json:"description"`
	IsVideo       bool          `json:"is_video"`
	Images        frameImages   `json:"images"`
	Videos        *videosData   `json:"videos"`
	CarouselData  *carouselData `json:"carousel_data"`
	StoryPinData  *storyData    `json:"story_pin_data"`
	DominantColor string        `json:"dominant_color"`
	CreatedAt     string        `json:"created_at"`
	Pinner        *struct {
		Username       string   `json:"username"`
		FullName       flexText `json:"full_name"`
		ImageMediumURL string   `json:"image_medium_url"`
	} `json:"pinner"`
	Board *struct {
		ID   string   `json:"id"`
		Name flexText `json:"name"`
		URL  string   `json:"url"`
	} `json:"board"`
}

type searchResponse struct {
	ResourceResponse struct {
		Data struct {
			Results []framePin `json:"results"`
		} `json:"data"`
	} `json:"resource_response"`
}

func (p framePin) toPin() *Pin {
	image := p.Images.best()
	isGif := strings.HasSuffix(strings.ToLower(image), ".gif")

	result := &Pin{
		ID:            p.ID,
		Title:         firstNonEmpty(p.Title.String(), p.GridTitle.String()),
		Description:   p.Description.String(),
		IsVideo:       p.IsVideo || p.Videos != nil,
		IsGif:         isGif,
		Image:         image,
		DominantColor: p.DominantColor,
		CreatedAt:     p.CreatedAt,
	}
	if p.ID != "" {
		result.URL = baseURL + "/pin/" + p.ID + "/"
	}

	if p.Pinner != nil {
		result.Pinner = &Pinner{
			Username: p.Pinner.Username,
			FullName: p.Pinner.FullName.String(),
			Avatar:   p.Pinner.ImageMediumURL,
		}
	}
	if p.Board != nil {
		boardURL := p.Board.URL
		if boardURL != "" && !strings.HasPrefix(boardURL, "http") {
			boardURL = baseURL + boardURL
		}
		result.Board = &Board{ID: p.Board.ID, Name: p.Board.Name.String(), URL: boardURL}
	}

	switch {
	case p.CarouselData != nil && len(p.CarouselData.Slots) > 0:
		appendCarousel(result, p.CarouselData.Slots)
	case p.StoryPinData != nil && len(p.StoryPinData.Pages) > 0:
		appendStory(result, p.StoryPinData.Pages)
	}
	// Slot carousel tanpa URL gambar tidak dihitung sebagai carousel.
	result.IsCarousel = len(result.Carousel) > 0

	switch {
	case result.IsCarousel:
		result.Type = "carousel"
	case result.IsVideo:
		result.Type = "video"
	case isGif:
		result.Type = "gif"
	default:
		result.Type = "image"
	}

	return result
}

func appendCarousel(result *Pin, slots []carouselSlot) {
	for i, slot := range slots {
		image := slot.Images.best()
		if image == "" {
			continue
		}
		result.Carousel = append(result.Carousel, Item{
			Index: i,
			Title: firstNonEmpty(slot.Title.String(), slot.Details.String()),
			URL:   image,
		})
	}
}

func appendStory(result *Pin, pages []storyPage) {
	for i, page := range pages {
		for _, block := range page.Blocks {
			if block.Image == nil {
				continue
			}
			image := firstNonEmpty(block.Image.Images.Originals.URL, block.Image.Images.Large.URL)
			if image == "" {
				continue
			}
			result.Carousel = append(result.Carousel, Item{
				Index: i,
				Title: page.Title.String(),
				URL:   image,
			})
		}
	}
}
