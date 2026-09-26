-- name: GetItem :one
SELECT * FROM items WHERE id = ?;

-- name: GetAllItems :many
SELECT * FROM  items;

-- name: CreateItem :exec
INSERT INTO items (id, name, filename, type) VALUES (?, ?, ?, ?);
