package main

import (
	"encoding/json/v2"
	"errors"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
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
	mux.HandleFunc("GET /api/toggle/{id}/{$}", toggleHandler)
	mux.HandleFunc("GET /api/settings/decoration/{$}", setDecorationHandler)
	mux.HandleFunc("GET /api/settings/taskbar/{$}", setTaskbarHandler)
	mux.HandleFunc("GET /api/settings/icon/{$}", setIconHandler)
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
	err := r.ParseMultipartForm(10 << 20) // 10 MiB
	if err != nil {
		log.Println("error parsing multipart form", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer LogFailedClose(r.MultipartForm.RemoveAll)

	var name string
	var file uuid.UUID
	var filename string
	Type := 0

	for k, v := range r.MultipartForm.Value {
		if k == "info" && len(v) == 1 {
			var res struct {
				Name string `json:"name"`
			}

			err := json.Unmarshal([]byte(v[0]), &res)
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			name = res.Name
		} else {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
	}

	for k, v := range r.MultipartForm.File {
		if k == "file" && len(v) == 1 {
			file, filename, Type, err = formFileSave(v[0])
			if err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

		} else {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
	}

	err = db.DB.CreateItem(r.Context(), db.CreateItemParams{
		ID:       file,
		Name:     name,
		Filename: filename,
		Type:     Type,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func formFileSave(part *multipart.FileHeader) (uuid.UUID, string, int, error) {
	file := uuid.NewV7()

	type_ := part.Header.Get("Content-Type")

	Type := 0
	switch type_ {
	case "image/png", "image/webp", "image/jpeg", "image/gif":
		Type = 1
	case "audio/vnd.wav", "audio/mpeg", "audio/ogg", "audio/flac": // TODO add more audio types
		Type = 2
	default:
		return uuid.Nil(), "", 0, errors.New("invalid file type")
	}

	inFile, err := part.Open()
	if err != nil {
		return uuid.Nil(), "", 0, err
	}
	defer LogFailedClose(inFile.Close)

	filename := file.String() + "_" + path.Base(part.Filename)

	root, err := os.OpenRoot(datadir)
	if err != nil {
		return uuid.Nil(), "", 0, err
	}
	defer LogFailedClose(root.Close)

	outFile, err := root.Create(filename)
	if err != nil {
		return uuid.Nil(), "", 0, err
	}
	defer LogFailedClose(outFile.Close)

	_, err = io.Copy(outFile, inFile)
	if err != nil {
		return uuid.Nil(), "", 0, err
	}
	return file, filename, Type, nil
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
