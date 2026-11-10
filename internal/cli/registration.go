package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"douxiyou.com/video-capture/internal/config"
	"douxiyou.com/video-capture/internal/face"
	"douxiyou.com/video-capture/internal/server"
	"douxiyou.com/video-capture/internal/storage"
)

func serveExperimental(ctx context.Context, cfg config.Config) (runErr error) {
	encryptionKey, err := base64.StdEncoding.DecodeString(cfg.Face.EncryptionKey)
	if err != nil || len(encryptionKey) != 32 {
		return errors.New("face.encryption_key (or FACE_ENCRYPTION_KEY) must be a base64-encoded 32-byte key")
	}
	jwtKey, err := base64.StdEncoding.DecodeString(cfg.Auth.JWTSigningKey)
	if err != nil || len(jwtKey) < 32 {
		return errors.New("auth.jwt_signing_key (or JWT_SIGNING_KEY) must be a base64-encoded key of at least 32 bytes")
	}
	if cfg.Face.ONNXLibrary == "" {
		return errors.New("face.onnx_library is required for experimental face registration")
	}
	if _, err := os.Stat(cfg.Database.Path); err != nil {
		return fmt.Errorf("database must already exist and be migrated: %w", err)
	}
	db, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	var versions int
	if err := db.GetContext(ctx, &versions, "SELECT COUNT(*) FROM schema_migrations WHERE version IN (1, 2)"); err != nil || versions != 2 {
		return errors.New("database is not migrated; run the migrate command first")
	}
	var uploader *storage.OSS
	if cfg.OSS.Enabled {
		var applied int
		if err := db.GetContext(ctx, &applied, "SELECT COUNT(*) FROM schema_migrations WHERE version = 3"); err != nil || applied != 1 {
			return errors.New("database migration 3 is required for OSS uploads; run the migrate command first")
		}
		uploader, err = storage.NewOSS(storage.OSSOptions{
			Region: cfg.OSS.Region, Bucket: cfg.OSS.Bucket, Endpoint: cfg.OSS.Endpoint,
			AccessKeyID: cfg.OSS.AccessKeyID, AccessKeySecret: cfg.OSS.AccessKeySecret,
			SessionToken: cfg.OSS.SessionToken,
		})
		if err != nil {
			return fmt.Errorf("configure OSS: %w", err)
		}
	}
	model, err := face.Open(cfg.Face.ONNXLibrary, cfg.Face.ModelPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := model.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	if uploader != nil {
		return server.ServeWithRegistration(ctx, cfg.Server.Addr, db, model, encryptionKey, jwtKey, cfg.Face.Threshold, uploader)
	}
	return server.ServeWithRegistration(ctx, cfg.Server.Addr, db, model, encryptionKey, jwtKey, cfg.Face.Threshold, nil)
}
