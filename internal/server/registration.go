package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"douxiyou.com/video-capture/internal/face"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/mojocn/base64Captcha"
)

type faceModel interface {
	Embed(image.Image) ([]float32, error)
}

type challenge struct {
	answer string
	ip     string
	expiry time.Time
}

type guest struct {
	ip     string
	expiry time.Time
}

type attempts struct {
	count int
	until time.Time
}

type registrationService struct {
	db         *sqlx.DB
	model      faceModel
	aead       cipher.AEAD
	jwtKey     []byte
	threshold  float64
	mu         sync.Mutex
	challenges map[string]challenge
	guests     map[[32]byte]guest
	rates      map[string]attempts
}

func newRegistrationService(db *sqlx.DB, model faceModel, encryptionKey, jwtKey []byte, threshold float64) (*registrationService, error) {
	if len(encryptionKey) != 32 || len(jwtKey) < 32 || math.IsNaN(threshold) || threshold <= 0 || threshold >= 1 {
		return nil, errors.New("encryption key must be 32 bytes, JWT key at least 32 bytes, and threshold between 0 and 1")
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &registrationService{db: db, model: model, aead: aead, jwtKey: jwtKey, threshold: threshold,
		challenges: make(map[string]challenge), guests: make(map[[32]byte]guest), rates: make(map[string]attempts)}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func peerIP(r *http.Request) (string, error) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(ip) == nil {
		return "", errors.New("invalid remote address")
	}
	return ip, nil
}

func (s *registrationService) allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, c := range s.challenges {
		if !now.Before(c.expiry) {
			delete(s.challenges, id)
		}
	}
	for id, g := range s.guests {
		if !now.Before(g.expiry) {
			delete(s.guests, id)
		}
	}
	for key, rate := range s.rates {
		if !now.Before(rate.until) {
			delete(s.rates, key)
		}
	}
	rate := s.rates[ip]
	if rate.until.IsZero() {
		if len(s.rates) >= 10000 {
			return false
		}
		rate.until = now.Add(time.Minute)
	}
	if rate.count >= 20 {
		return false
	}
	rate.count++
	s.rates[ip] = rate
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Headers have already been sent; surface the write failure to the server log.
		log.Printf("write response: %v", err)
	}
}

func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid JSON body")
	}
	return nil
}

func (s *registrationService) captcha(w http.ResponseWriter, r *http.Request) {
	ip, err := peerIP(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid client address")
		return
	}
	if !s.allow(ip) {
		fail(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	id, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "captcha unavailable")
		return
	}
	var answer strings.Builder
	for i := 0; i < 6; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			fail(w, http.StatusInternalServerError, "captcha unavailable")
			return
		}
		answer.WriteString(n.String())
	}
	driver := base64Captcha.NewDriverDigit(80, 240, 6, 0.7, 80)
	item, err := driver.DrawCaptcha(answer.String())
	if err != nil {
		fail(w, http.StatusInternalServerError, "captcha unavailable")
		return
	}
	s.mu.Lock()
	if len(s.challenges) >= 10000 {
		s.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "captcha capacity reached")
		return
	}
	s.challenges[id] = challenge{answer.String(), ip, time.Now().Add(2 * time.Minute)}
	s.mu.Unlock()
	respond(w, http.StatusOK, map[string]string{"captcha_id": id, "image": item.EncodeB64string()})
}

func (s *registrationService) issueGuest(w http.ResponseWriter, r *http.Request) {
	ip, err := peerIP(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid client address")
		return
	}
	if !s.allow(ip) {
		fail(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	var req struct {
		CaptchaID string `json:"captcha_id"`
		Answer    string `json:"answer"`
	}
	if err := readJSON(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	s.mu.Lock()
	c, ok := s.challenges[req.CaptchaID]
	delete(s.challenges, req.CaptchaID)
	s.mu.Unlock()
	if !ok || c.ip != ip || !time.Now().Before(c.expiry) || subtle.ConstantTimeCompare([]byte(c.answer), []byte(strings.TrimSpace(req.Answer))) != 1 {
		fail(w, http.StatusUnauthorized, "invalid or expired captcha")
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(w, http.StatusInternalServerError, "guest token unavailable")
		return
	}
	s.mu.Lock()
	if len(s.guests) >= 10000 {
		s.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "guest capacity reached")
		return
	}
	s.guests[sha256.Sum256([]byte(token))] = guest{ip, time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	respond(w, http.StatusOK, map[string]any{"guest_token": token, "expires_in": 300})
}

func (s *registrationService) consumeGuest(r *http.Request, ip string) bool {
	auth := strings.Fields(r.Header.Get("Authorization"))
	if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") {
		return false
	}
	key := sha256.Sum256([]byte(auth[1]))
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guests[key]
	delete(s.guests, key)
	return ok && g.ip == ip && time.Now().Before(g.expiry)
}

func normalizeEmbedding(vector []float32) ([]float32, error) {
	if len(vector) != face.Dimension {
		return nil, errors.New("unexpected face embedding dimension")
	}
	var sum float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, errors.New("invalid face embedding")
		}
		sum += float64(v) * float64(v)
	}
	if sum < 1e-12 {
		return nil, errors.New("empty face embedding")
	}
	scale := math.Sqrt(sum)
	out := make([]float32, len(vector))
	for i, v := range vector {
		out[i] = float32(float64(v) / scale)
	}
	return out, nil
}

func (s *registrationService) encrypt(vector []float32) ([]byte, error) {
	b := make([]byte, len(vector)*4)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, b, []byte(face.ModelID)), nil
}

func (s *registrationService) decrypt(data []byte) ([]float32, error) {
	n := s.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted face embedding")
	}
	b, err := s.aead.Open(nil, data[:n], data[n:], []byte(face.ModelID))
	if err != nil {
		return nil, err
	}
	if len(b) != face.Dimension*4 {
		return nil, errors.New("invalid stored face embedding")
	}
	vector := make([]float32, face.Dimension)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return normalizeEmbedding(vector)
}

var errDuplicate = errors.New("face or username already registered")
var errUnverifiedUsers = errors.New("existing users without face embeddings")

func (s *registrationService) saveUser(ctx context.Context, name, credentialID string, vector []float32) (int64, error) {
	encrypted, err := s.encrypt(vector)
	if err != nil {
		return 0, err
	}
	conn, err := s.db.Connx(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				log.Printf("rollback registration: %v", err)
			}
		}
	}()
	var missing int
	if err := conn.GetContext(ctx, &missing, `SELECT COUNT(*) FROM users u LEFT JOIN face_embeddings f ON f.user_id=u.id WHERE f.user_id IS NULL`); err != nil {
		return 0, err
	}
	if missing > 0 {
		return 0, errUnverifiedUsers
	}
	rows, err := conn.QueryxContext(ctx, "SELECT model_id, encrypted_embedding FROM face_embeddings")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var modelID string
		var ciphertext []byte
		if err := rows.Scan(&modelID, &ciphertext); err != nil {
			rows.Close()
			return 0, err
		}
		if modelID != face.ModelID {
			rows.Close()
			return 0, errors.New("incompatible face embedding model")
		}
		other, err := s.decrypt(ciphertext)
		if err != nil {
			rows.Close()
			return 0, err
		}
		var similarity float64
		for i, v := range vector {
			similarity += float64(v) * float64(other[i])
		}
		if similarity >= s.threshold {
			rows.Close()
			return 0, errDuplicate
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var names int
	if err := conn.GetContext(ctx, &names, "SELECT COUNT(*) FROM users WHERE name = ?", name); err != nil {
		return 0, err
	}
	if names != 0 {
		return 0, errDuplicate
	}
	result, err := conn.ExecContext(ctx, "INSERT INTO users (name, credential_id) VALUES (?, ?)", name, credentialID)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO face_embeddings (user_id, model_id, encrypted_embedding) VALUES (?, ?, ?)", id, face.ModelID, encrypted); err != nil {
		return 0, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, err
	}
	committed = true
	return id, nil
}

func (s *registrationService) readFaceRequest(w http.ResponseWriter, r *http.Request) (string, []float32, bool) {
	ip, err := peerIP(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid client address")
		return "", nil, false
	}
	if !s.consumeGuest(r, ip) {
		fail(w, http.StatusUnauthorized, "invalid or expired guest token")
		return "", nil, false
	}
	var req struct {
		Name  string `json:"name"`
		Image string `json:"image"`
	}
	if err := readJSON(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return "", nil, false
	}
	name := strings.TrimSpace(req.Name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 30 {
		fail(w, http.StatusBadRequest, "name must contain 1 to 30 characters")
		return "", nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(req.Image)
	if err != nil || len(raw) > 128<<10 {
		fail(w, http.StatusBadRequest, "invalid image")
		return "", nil, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") || config.Width != 112 || config.Height != 112 {
		fail(w, http.StatusBadRequest, "image must be a 112x112 JPEG or PNG")
		return "", nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid image")
		return "", nil, false
	}
	vector, err := s.model.Embed(img)
	if err != nil {
		log.Printf("face model error: %v", err)
		fail(w, http.StatusServiceUnavailable, "face model unavailable")
		return "", nil, false
	}
	vector, err = normalizeEmbedding(vector)
	if err != nil {
		log.Printf("invalid face model output: %v", err)
		fail(w, http.StatusServiceUnavailable, "face model unavailable")
		return "", nil, false
	}
	return name, vector, true
}

func (s *registrationService) issueUserToken(w http.ResponseWriter, status int, id int64, name, credentialID string) {
	now := time.Now()
	claims := jwt.RegisteredClaims{Issuer: "video-capture", ID: credentialID, Subject: strconv.FormatInt(id, 10), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour))}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtKey)
	if err != nil {
		log.Printf("sign user token: %v", err)
		fail(w, http.StatusInternalServerError, "unable to issue user token")
		return
	}
	respond(w, status, map[string]any{"user_id": id, "name": name, "user_token": token, "expires_in": 86400})
}

func (s *registrationService) register(w http.ResponseWriter, r *http.Request) {
	name, vector, ok := s.readFaceRequest(w, r)
	if !ok {
		return
	}
	credentialID, err := randomToken()
	if err != nil {
		log.Printf("generate user credential: %v", err)
		fail(w, http.StatusInternalServerError, "unable to create user")
		return
	}
	id, err := s.saveUser(r.Context(), name, credentialID, vector)
	if errors.Is(err, errDuplicate) {
		fail(w, http.StatusConflict, "face or username already registered")
		return
	}
	if err != nil {
		log.Printf("register user: %v", err)
		fail(w, http.StatusServiceUnavailable, "registration unavailable")
		return
	}
	s.issueUserToken(w, http.StatusCreated, id, name, credentialID)
}

func (s *registrationService) login(w http.ResponseWriter, r *http.Request) {
	name, vector, ok := s.readFaceRequest(w, r)
	if !ok {
		return
	}
	var record struct {
		ID         int64  `db:"id"`
		Credential string `db:"credential_id"`
		Model      string `db:"model_id"`
		Embedding  []byte `db:"encrypted_embedding"`
	}
	err := s.db.GetContext(r.Context(), &record, `SELECT u.id, u.credential_id, f.model_id, f.encrypted_embedding
		FROM users u JOIN face_embeddings f ON f.user_id = u.id WHERE u.name = ?`, name)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusUnauthorized, "invalid user or face")
		return
	}
	if err != nil || record.Model != face.ModelID || record.Credential == "" {
		log.Printf("face login lookup failed: %v", err)
		fail(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	other, err := s.decrypt(record.Embedding)
	if err != nil {
		log.Printf("face login decrypt failed: %v", err)
		fail(w, http.StatusServiceUnavailable, "login unavailable")
		return
	}
	var similarity float64
	for i, v := range vector {
		similarity += float64(v) * float64(other[i])
	}
	if similarity < s.threshold {
		fail(w, http.StatusUnauthorized, "invalid user or face")
		return
	}
	s.issueUserToken(w, http.StatusOK, record.ID, name, record.Credential)
}

func (s *registrationService) userID(r *http.Request) (int64, string, error) {
	auth := strings.Fields(r.Header.Get("Authorization"))
	if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") {
		return 0, "", errors.New("invalid user token")
	}
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(auth[1], claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return s.jwtKey, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("video-capture"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return 0, "", errors.New("invalid user token")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id < 1 || claims.ID == "" {
		return 0, "", errors.New("invalid user token")
	}
	return id, claims.ID, nil
}

func (s *registrationService) getUser(w http.ResponseWriter, r *http.Request) {
	id, credentialID, err := s.userID(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "invalid user token")
		return
	}
	var name string
	if err := s.db.GetContext(r.Context(), &name, "SELECT name FROM users WHERE id = ? AND credential_id = ?", id, credentialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusUnauthorized, "invalid user token")
		} else {
			log.Printf("get user: %v", err)
			fail(w, http.StatusServiceUnavailable, "user lookup unavailable")
		}
		return
	}
	respond(w, http.StatusOK, map[string]any{"user_id": id, "name": name})
}

func (s *registrationService) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, credentialID, err := s.userID(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "invalid user token")
		return
	}
	result, err := s.db.ExecContext(r.Context(), "DELETE FROM users WHERE id = ? AND credential_id = ?", id, credentialID)
	if err != nil {
		log.Printf("delete user: %v", err)
		fail(w, http.StatusServiceUnavailable, "user could not be deleted")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		fail(w, http.StatusInternalServerError, "unable to delete user")
		return
	}
	if count == 0 {
		fail(w, http.StatusUnauthorized, "invalid user token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
