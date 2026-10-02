package main

import (
	"encoding/json/v2"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path"
	"slices"
	"time"
	"uuid"
	"wayland/db"
)

const datadir = "data"

func registerApi(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/items/{$}", getAll)
	mux.HandleFunc("POST /api/items/{$}", addItem)
	mux.HandleFunc("GET /api/file/{id}/{$}", getFile)
	mux.HandleFunc("POST /api/toggle/{id}/{$}", toggleHandler)
	mux.HandleFunc("POST /api/settings/decoration/{$}", setDecorationHandler)
	mux.HandleFunc("POST /api/settings/taskbar/{$}", setTaskbarHandler)
	mux.HandleFunc("POST /api/settings/icon/{$}", setIconHandler)
	mux.HandleFunc("POST /api/set/emoji/{emoji}/{$}", handleSetEmoji)
}

func getFile(w http.ResponseWriter, r *http.Request) {
	idRaw := r.PathValue("id")
	id, err := uuid.Parse(idRaw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	item, err := db.DB.GetItem(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.ServeFile(w, r, path.Join(datadir, item.Filename))
}

func getAll(w http.ResponseWriter, r *http.Request) {
	items, err := db.DB.GetAllItems(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slices.SortFunc(items, func(a, b db.Item) int {
		return a.Order - b.Order
	})

	type result struct {
		ID      string    `json:"id"`
		Name    string    `json:"name"`
		Created time.Time `json:"created"`
		Type    int       `json:"type"`
	}

	var res []result
	for _, item := range items {
		res = append(res, result{
			ID:      item.ID.String(),
			Name:    item.Name,
			Created: item.Created,
			Type:    item.Type,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.MarshalWrite(w, res)
}

func addItem(w http.ResponseWriter, r *http.Request) {
	name := r.Header.Get("Name")
	id := uuid.NewV7()

	if name == "" {
		name = id.String()
	}

	var fileExt string
	var type_ int
	switch r.Header.Get("Content-Type") {
	case "image/png":
		fileExt = ".png"
		type_ = 1
	case "image/webp":
		fileExt = ".webp"
		type_ = 1
	case "image/jpeg":
		fileExt = ".jpeg"
		type_ = 1
	case "image/gif":
		fileExt = ".gif"
		type_ = 1
	case "audio/vnd.wav":
		fileExt = ".wav"
		type_ = 2
	case "audio/mpeg":
		fileExt = ".mp3"
		type_ = 2
	case "audio/ogg":
		fileExt = ".ogg"
		type_ = 2
	case "audio/flac":
		fileExt = ".flac"
		type_ = 2
	default:
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	f, err := os.Create(path.Join(datadir, id.String()+fileExt))
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer LogFailedClose(f.Close)

	_, err = io.Copy(f, r.Body)
	if err != nil {
		log.Println(err)
	}

	err = db.DB.CreateItem(r.Context(), db.CreateItemParams{
		ID:       id,
		Name:     name,
		Filename: id.String() + fileExt,
		Type:     type_,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func toggleHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = toggle(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func setDecorationHandler(_ http.ResponseWriter, r *http.Request) {
	deco := r.URL.Query().Get("deco") == "true"
	setDecoration(deco)
}

func setTaskbarHandler(_ http.ResponseWriter, r *http.Request) {
	deco := r.URL.Query().Get("taskbar") == "true"
	setTaskbar(deco)
}

func setIconHandler(w http.ResponseWriter, r *http.Request) {
	icon := r.URL.Query().Get("icon")
	if icon == "0" {
		toggleIcon("templates/KEKW.webp")
		return
	}

	id, err := uuid.Parse(icon)
	if err != nil {
		slog.Info(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	item, err := db.DB.GetItem(r.Context(), id)
	if err != nil {
		slog.Info(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if item.Type != 1 {
		return
	}

	toggleIcon(path.Join(datadir, item.Filename))
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleSetEmoji(_ http.ResponseWriter, r *http.Request) {
	emoji := r.PathValue("emoji")
	setEmoji(emoji)
}
