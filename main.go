package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"time"
	"uuid"
	"wayland/db"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "github.com/gen2brain/vpx/webp"
)

var settings Settings

func main() {
	settings.Taskbar = true
	settings.Icon = "templates/KEKW.webp"
	startup()

	slog.SetLogLoggerLevel(slog.LevelInfo)

	DB, err := db.Init(path.Join(datadir, "db.sqlite"))
	if err != nil {
		log.Fatal(err)
	}
	defer LogFailedClose(DB.Close)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)

	s, err := waylandDisplayConnect()
	surface = &s
	if err != nil {
		log.Fatal(err)
	}
	defer LogFailedClose(s.Close)

	server := createServer()
	go func() {
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	fmt.Println("Server started")
	fmt.Println("http://localhost:8080/")

	s.waylandWLDisplayGetRegistry()

	go s.readMessages(cancel)

	<-ctx.Done()
	fmt.Println("stopping")
	shtctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(shtctx)
	LogFailedClose(s.Close)
}

func startup() {
	dirs := []string{"data"}
	for _, dir := range dirs {
		err := os.Mkdir(dir, 0755)
		if err != nil && !os.IsExist(err) {
			log.Fatal(err)
		}
	}
}

var surface *state

func toggle(id uuid.UUID) error {
	item, err := db.DB.GetItem(context.Background(), id)
	if err != nil {
		return errors.New("item doesn't exists")
	}

	if surface == nil {
		return errors.New("surface is nil")
	}

	if surface.wlSurface == 0 {
		surface.createWindow()
	} else if settings.content.name == item.ID.String() {
		surface.waylandDestroyWindow()
		settings.content.name = ""
		settings.content.source = nil
		if settings.content.cancel != nil {
			settings.content.cancel()
			settings.content.cancel = nil
		}
		return nil
	}
	surface.renderImage(path.Join(datadir, item.Filename), item.ID.String())
	return nil
}

func setEmoji(emoji string) {
	if surface.wlSurface == 0 {
		surface.createWindow()
	} else if settings.content.name == string(emoji) {
		surface.waylandDestroyWindow()
		settings.content.name = ""
		settings.content.source = nil
		if settings.content.cancel != nil {
			settings.content.cancel()
			settings.content.cancel = nil
		}
		return
	}
	surface.renderEmoji(emoji)
}

func setDecoration(deco bool) {
	current := settings.Decorations
	settings.Decorations = deco
	if current != deco && surface.xdgToplevel != 0 {
		if deco {
			surface.waylandZxdgToplevelDecorationSetMode(waylandDecoModeServerSide)
		} else {
			surface.waylandZxdgToplevelDecorationSetMode(waylandDecoModeClientSide)
		}
	}
}

func setTaskbar(visible bool) {
	current := settings.Taskbar
	settings.Taskbar = visible
	if current != visible && surface.xdgToplevel != 0 {
		surface.plasmaSurfaceSetSkipTaskbar(!visible)
		surface.iconManagerSetIcon(surface.xdgToplevel, surface.iconInstant)
	}
}

func toggleIcon(filename string) {
	if filename == settings.Icon {
		return
	}
	settings.Icon = filename
	surface.switchIcon()
	slog.Info("Icon changed to: " + filename)
}

func createServer() *http.Server {
	mux := http.NewServeMux()
	registerApi(mux)

	t, err := template.New("").ParseFS(embedded, "templates/*.gohtml")
	if err != nil {
		panic(err)
	}

	mux.HandleFunc("GET /sound.svg", func(w http.ResponseWriter, _ *http.Request) {
		f, _ := embedded.Open("templates/sound.svg")
		defer LogFailedClose(f.Close)
		w.Header().Set("Content-Type", "image/svg+xml")
		io.Copy(w, f)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		items, err := db.DB.GetAllItems(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		slices.SortFunc(items, func(a, b db.Item) int {
			return a.Order - b.Order
		})

		err = t.ExecuteTemplate(w, "index.gohtml", items)
		if err != nil {
			log.Fatal(err)
			return
		}

	})

	server := http.Server{
		Addr:    ":8080",
		Handler: CORSWrapper(mux),
	}

	return new(server)
}

func CORSWrapper(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	})
}

type Settings struct {
	Taskbar bool
	Icon    string

	Decorations bool
	content     Content
}

//go:embed templates
var embedded embed.FS
