package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"gowa/internal/cfc"
	"gowa/internal/command"
	"gowa/internal/dola"
	"gowa/internal/facebook"
	"gowa/internal/hd"
	"gowa/internal/instagram"
	"gowa/internal/pinterest"
	"gowa/internal/soundcloud"
	"gowa/internal/sticker"
	"gowa/internal/tiktok"
	"gowa/internal/tts"
	"gowa/internal/util"
	"gowa/internal/xscraper"
	"gowa/internal/ytmusic"
)

type Info struct {
	Name  string
	Cmd   string
	Categ string
	Desc  string
	Alias []string
}

var Exports = interp.Exports{
	"gowa/internal/plugin/plugin": {
		"Info":     reflect.ValueOf((*Info)(nil)),
		"Register": reflect.ValueOf(Register),
	},
	"gowa/internal/command/command": {
		"Context":   reflect.ValueOf((*command.Context)(nil)),
		"AlbumItem": reflect.ValueOf((*command.AlbumItem)(nil)),
	},
	"gowa/internal/tiktok/tiktok": {
		"Metadata":        reflect.ValueOf((*tiktok.Metadata)(nil)),
		"Music":           reflect.ValueOf((*tiktok.Music)(nil)),
		"Profile":         reflect.ValueOf((*tiktok.Profile)(nil)),
		"Fetch":           reflect.ValueOf(tiktok.Fetch),
		"FetchProfile":    reflect.ValueOf(tiktok.FetchProfile),
		"FormatProfile":   reflect.ValueOf(tiktok.FormatProfile),
		"ExtractID":       reflect.ValueOf(tiktok.ExtractID),
		"ExtractUsername": reflect.ValueOf(tiktok.ExtractUsername),
		"DownloadBytes":   reflect.ValueOf(tiktok.DownloadBytes),
	},
	"gowa/internal/ytmusic/ytmusic": {
		"Track":        reflect.ValueOf((*ytmusic.Track)(nil)),
		"Audio":        reflect.ValueOf((*ytmusic.Audio)(nil)),
		"Video":        reflect.ValueOf((*ytmusic.Video)(nil)),
		"Search":       reflect.ValueOf(ytmusic.Search),
		"FetchAudio":   reflect.ValueOf(ytmusic.FetchAudio),
		"FetchVideo":   reflect.ValueOf(ytmusic.FetchVideo),
		"ParseVideoID": reflect.ValueOf(ytmusic.ParseVideoID),
	},
	"gowa/internal/facebook/facebook": {
		"Metadata":      reflect.ValueOf((*facebook.Metadata)(nil)),
		"ExtractURL":    reflect.ValueOf(facebook.ExtractURL),
		"Fetch":         reflect.ValueOf(facebook.Fetch),
		"DownloadBytes": reflect.ValueOf(facebook.DownloadBytes),
	},
	"gowa/internal/soundcloud/soundcloud": {
		"Track":        reflect.ValueOf((*soundcloud.Track)(nil)),
		"SearchResult": reflect.ValueOf((*soundcloud.SearchResult)(nil)),
		"Audio":        reflect.ValueOf((*soundcloud.Audio)(nil)),
		"Search":       reflect.ValueOf(soundcloud.Search),
		"FormatSearch": reflect.ValueOf(soundcloud.FormatSearch),
		"FetchAudio":   reflect.ValueOf(soundcloud.FetchAudio),
	},
	"gowa/internal/xscraper/xscraper": {
		"Metadata":      reflect.ValueOf((*xscraper.Metadata)(nil)),
		"Author":        reflect.ValueOf((*xscraper.Author)(nil)),
		"Media":         reflect.ValueOf((*xscraper.Media)(nil)),
		"Result":        reflect.ValueOf((*xscraper.Result)(nil)),
		"ParseArgs":     reflect.ValueOf(xscraper.ParseArgs),
		"ExtractLink":   reflect.ValueOf(xscraper.ExtractLink),
		"Fetch":         reflect.ValueOf(xscraper.Fetch),
		"FormatCaption": reflect.ValueOf(xscraper.FormatCaption),
	},
	"gowa/internal/pinterest/pinterest": {
		"Pin":           reflect.ValueOf((*pinterest.Pin)(nil)),
		"Pinner":        reflect.ValueOf((*pinterest.Pinner)(nil)),
		"Board":         reflect.ValueOf((*pinterest.Board)(nil)),
		"Video":         reflect.ValueOf((*pinterest.Video)(nil)),
		"Item":          reflect.ValueOf((*pinterest.Item)(nil)),
		"SearchResult":  reflect.ValueOf((*pinterest.SearchResult)(nil)),
		"Fetch":         reflect.ValueOf(pinterest.Fetch),
		"Search":        reflect.ValueOf(pinterest.Search),
		"DownloadBytes": reflect.ValueOf(pinterest.DownloadBytes),
		"FormatSearch":  reflect.ValueOf(pinterest.FormatSearch),
		"ExtractPinID":  reflect.ValueOf(pinterest.ExtractPinID),
		"MIME":          reflect.ValueOf(pinterest.MIME),
	},
	"gowa/internal/hd/hd": {
		"UploadToCDN":  reflect.ValueOf(hd.UploadToCDN),
		"EnhanceImage": reflect.ValueOf(hd.EnhanceImage),
	},
	"gowa/internal/dola/dola": {
		"Reply": reflect.ValueOf((*dola.Reply)(nil)),
		"Ask":   reflect.ValueOf(dola.Ask),
		"Reset": reflect.ValueOf(dola.Reset),
	},
	"gowa/internal/sticker/sticker": {
		"Send": reflect.ValueOf(sticker.Send),
	},
	"gowa/internal/tts/tts": {
		"ErrEmptyText": reflect.ValueOf(tts.ErrEmptyText),
		"CleanText":    reflect.ValueOf(tts.CleanText),
		"SplitChunks":  reflect.ValueOf(tts.SplitChunks),
		"Synthesize":   reflect.ValueOf(tts.Synthesize),
	},
	"gowa/internal/cfc/cfc": {
		"Spec": reflect.ValueOf((*cfc.Spec)(nil)),
		"Send": reflect.ValueOf(cfc.Send),
	},
	"gowa/internal/instagram/instagram": {
		"Metadata":               reflect.ValueOf((*instagram.Metadata)(nil)),
		"Item":                   reflect.ValueOf((*instagram.Item)(nil)),
		"Profile":                reflect.ValueOf((*instagram.Profile)(nil)),
		"SearchResult":           reflect.ValueOf((*instagram.SearchResult)(nil)),
		"ErrSearchLoginRequired": reflect.ValueOf(instagram.ErrSearchLoginRequired),
		"ErrTagNotFound":         reflect.ValueOf(instagram.ErrTagNotFound),
		"ErrUserNotFound":        reflect.ValueOf(instagram.ErrUserNotFound),
		"ErrPrivateAccount":      reflect.ValueOf(instagram.ErrPrivateAccount),
		"Search":                 reflect.ValueOf(instagram.Search),
		"FetchUserPosts":         reflect.ValueOf(instagram.FetchUserPosts),
		"ResolveUsername":        reflect.ValueOf(instagram.ResolveUsername),
		"Fetch":                  reflect.ValueOf(instagram.Fetch),
		"FetchProfile":           reflect.ValueOf(instagram.FetchProfile),
		"FormatProfile":          reflect.ValueOf(instagram.FormatProfile),
		"ExtractCode":            reflect.ValueOf(instagram.ExtractCode),
		"ExtractUsername":        reflect.ValueOf(instagram.ExtractUsername),
		"DownloadBytes":          reflect.ValueOf(instagram.DownloadBytes),
		"DownloadMedia":          reflect.ValueOf(instagram.DownloadMedia),
	},
}

type loader struct {
	file string
	ip   *interp.Interpreter
	mu   sync.Mutex
	n    int
}

var (
	mu      sync.Mutex
	loadMu  sync.Mutex
	active  *loader
	keepAlv []*loader
)

func Register(info Info, handler func(ctx *command.Context) error) {
	mu.Lock()
	l := active
	mu.Unlock()

	if l == nil {
		util.Error("Plugin", "Register dipanggil di luar proses load plugin, perintah %q diabaikan", info.Cmd)
		return
	}

	file := filepath.Base(l.file)
	cmdName := strings.ToLower(strings.TrimSpace(info.Cmd))
	if cmdName == "" {
		util.Error("Plugin", "%s: perintah tanpa Cmd diabaikan", file)
		return
	}
	if handler == nil {
		util.Error("Plugin", "%s: perintah %q tanpa handler diabaikan", file, cmdName)
		return
	}
	if _, exists := command.Find(cmdName); exists {
		util.Error("Plugin", "%s: perintah %q sudah terdaftar, dilewati", file, cmdName)
		return
	}

	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = cmdName
	}

	categ := strings.TrimSpace(info.Categ)
	if categ == "" {
		categ = "plugin"
	}

	desc := strings.TrimSpace(info.Desc)
	if desc == "" {
		desc = "Perintah dari plugin " + file
	}

	aliases := make([]string, 0, len(info.Alias))
	for _, alias := range info.Alias {
		clean := strings.ToLower(strings.TrimSpace(alias))
		if clean == "" || clean == cmdName {
			continue
		}
		if _, exists := command.Find(clean); exists {
			util.Error("Plugin", "%s: alias %q sudah dipakai, dilewati", file, clean)
			continue
		}
		aliases = append(aliases, clean)
	}

	wrapped := func(ctx *command.Context) (err error) {
		defer func() {
			if r := recover(); r != nil {
				util.Error("Plugin", "Panic pada %s (%s): %v", file, cmdName, r)
				err = fmt.Errorf("plugin %s gagal mengeksekusi %s", file, cmdName)
			}
		}()
		return handler(ctx)
	}

	source, _ := filepath.Abs(l.file)

	command.Register(&command.Command{
		Name:    name,
		Cmd:     cmdName,
		Categ:   categ,
		Desc:    desc,
		Alias:   aliases,
		Source:  source,
		Execute: wrapped,
	})

	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func LoadDir(dir string) int {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "plugins"
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			util.Info("Plugin", "Direktori %s belum ada, tidak ada plugin yang dimuat", dir)
			return 0
		}
		util.Error("Plugin", "Gagal membaca direktori %s: %v", dir, err)
		return 0
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)

	if len(files) == 0 {
		return 0
	}

	total := 0
	loaded := 0
	for _, file := range files {
		n, err := loadFile(file)
		if err != nil {
			util.Error("Plugin", "Gagal memuat %s: %v", filepath.Base(file), err)
			continue
		}
		loaded++
		total += n
	}

	if total > 0 {
		util.Success("Plugin", "%d perintah dimuat dari %d plugin di %s", total, loaded, dir)
	}
	return total
}

func LoadFile(path string) (int, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loadFile(path)
}

func loadFile(path string) (int, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	ip := interp.New(interp.Options{})
	if err := ip.Use(stdlib.Symbols); err != nil {
		return 0, err
	}
	if err := ip.Use(Exports); err != nil {
		return 0, err
	}

	l := &loader{file: path, ip: ip}

	mu.Lock()
	if active != nil {
		mu.Unlock()
		return 0, fmt.Errorf("plugin lain sedang dimuat")
	}
	active = l
	mu.Unlock()

	defer func() {
		mu.Lock()
		active = nil
		mu.Unlock()
	}()

	if _, err := ip.Eval(string(source)); err != nil {
		return 0, err
	}

	if l.n == 0 {
		return 0, fmt.Errorf("tidak ada perintah yang didaftarkan, panggil plugin.Register di dalam init()")
	}

	mu.Lock()
	keepAlv = append(keepAlv, l)
	mu.Unlock()

	return l.n, nil
}
