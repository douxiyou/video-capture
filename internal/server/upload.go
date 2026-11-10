package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxImageBytes = 5 << 20
const maxUploadBytes = maxImageBytes + 64<<10

type imageUpload struct {
	data        []byte
	filename    string
	contentType string
	expression  string
	extension   string
}

func parseImageUpload(w http.ResponseWriter, r *http.Request) (imageUpload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return imageUpload{}, fmt.Errorf("expected multipart/form-data")
	}
	var result imageUpload
	seenFile, seenExpression := false, false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return imageUpload{}, fmt.Errorf("invalid multipart body: %w", err)
		}
		switch part.FormName() {
		case "file":
			if seenFile || part.FileName() == "" {
				return imageUpload{}, errors.New("exactly one image file is required")
			}
			seenFile = true
			filename := part.FileName()
			if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
				filename = filename[i+1:]
			}
			if !utf8.ValidString(filename) || filename == "" || len(filename) > 255 || strings.ContainsAny(filename, "\x00\r\n") {
				return imageUpload{}, errors.New("invalid filename")
			}
			result.filename = filename
			result.data, err = io.ReadAll(io.LimitReader(part, maxImageBytes+1))
			if err != nil {
				return imageUpload{}, fmt.Errorf("read image: %w", err)
			}
			if len(result.data) == 0 || len(result.data) > maxImageBytes {
				return imageUpload{}, errors.New("image must be between 1 byte and 5 MiB")
			}
		case "expression":
			if seenExpression || part.FileName() != "" {
				return imageUpload{}, errors.New("invalid expression")
			}
			seenExpression = true
			value, err := io.ReadAll(io.LimitReader(part, 256))
			if err != nil {
				return imageUpload{}, fmt.Errorf("read expression: %w", err)
			}
			result.expression = strings.TrimSpace(string(value))
			if len(value) == 256 || !utf8.ValidString(result.expression) || utf8.RuneCountInString(result.expression) > 30 || strings.ContainsAny(result.expression, "\x00\r\n") {
				return imageUpload{}, errors.New("expression must be at most 30 characters")
			}
		default:
			return imageUpload{}, errors.New("unexpected multipart field")
		}
		if err := part.Close(); err != nil {
			return imageUpload{}, fmt.Errorf("close multipart part: %w", err)
		}
	}
	if !seenFile {
		return imageUpload{}, errors.New("image file is required")
	}
	info, format, err := image.DecodeConfig(bytes.NewReader(result.data))
	if err != nil || (format != "jpeg" && format != "png") || info.Width < 1 || info.Height < 1 ||
		info.Width > 4096 || info.Height > 4096 || int64(info.Width)*int64(info.Height) > 16_000_000 {
		return imageUpload{}, errors.New("image must be a JPEG or PNG of at most 4096x4096 and 16 million pixels")
	}
	if _, _, err := image.Decode(bytes.NewReader(result.data)); err != nil {
		return imageUpload{}, errors.New("image data is invalid")
	}
	if format == "jpeg" {
		result.contentType, result.extension = "image/jpeg", ".jpg"
	} else {
		result.contentType, result.extension = "image/png", ".png"
	}
	return result, nil
}

func (s *registrationService) uploadImage(uploader imageUploader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, credential, err := s.userID(r)
		if err != nil {
			fail(w, http.StatusUnauthorized, "invalid user token")
			return
		}
		var active int
		if err := s.db.GetContext(r.Context(), &active, "SELECT 1 FROM users WHERE id = ? AND credential_id = ?", id, credential); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, http.StatusUnauthorized, "invalid user token")
			} else {
				log.Printf("check upload user: %v", err)
				fail(w, http.StatusServiceUnavailable, "user lookup unavailable")
			}
			return
		}
		upload, err := parseImageUpload(w, r)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		random, err := randomToken()
		if err != nil {
			log.Printf("generate image key: %v", err)
			fail(w, http.StatusInternalServerError, "unable to create upload")
			return
		}
		key := "images/" + strconv.FormatInt(id, 10) + "/" + random + upload.extension
		result, err := s.db.ExecContext(r.Context(), `INSERT INTO image_uploads
			(user_id, file_path, original_filename, content_type, size_bytes, expression, status)
			SELECT id, ?, ?, ?, ?, ?, 'pending' FROM users WHERE id = ? AND credential_id = ?`,
			key, upload.filename, upload.contentType, len(upload.data), upload.expression, id, credential)
		if err != nil {
			log.Printf("reserve image upload: %v", err)
			fail(w, http.StatusServiceUnavailable, "unable to create upload record")
			return
		}
		rows, err := result.RowsAffected()
		if err != nil {
			log.Printf("read upload result: %v", err)
			fail(w, http.StatusServiceUnavailable, "unable to create upload record")
			return
		}
		if rows != 1 {
			fail(w, http.StatusUnauthorized, "invalid user token")
			return
		}
		recordID, err := result.LastInsertId()
		if err != nil {
			log.Printf("read upload id for %s: %v", key, err)
			fail(w, http.StatusServiceUnavailable, "upload record requires reconciliation")
			return
		}
		if err := uploader.Put(r.Context(), key, upload.contentType, upload.data); err != nil {
			log.Printf("upload %d to OSS failed; outcome may be unknown: %v", recordID, err)
			respond(w, http.StatusBadGateway, map[string]any{"error": "upload outcome unknown; record requires reconciliation", "upload_id": recordID, "status": "pending"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err = s.db.ExecContext(ctx, "UPDATE image_uploads SET status = 'uploaded' WHERE id = ? AND status = 'pending'", recordID)
		if err == nil {
			rows, err = result.RowsAffected()
			if err == nil && rows != 1 {
				err = errors.New("pending upload record not found")
			}
		}
		if err != nil {
			log.Printf("finalize upload %d: %v", recordID, err)
			respond(w, http.StatusServiceUnavailable, map[string]any{"error": "image uploaded but record requires reconciliation", "upload_id": recordID, "status": "pending"})
			return
		}
		respond(w, http.StatusCreated, map[string]any{"id": recordID, "status": "uploaded", "content_type": upload.contentType, "size_bytes": len(upload.data), "expression": upload.expression})
	}
}
