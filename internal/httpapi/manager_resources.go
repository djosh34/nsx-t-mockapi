package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

func managerPayloadID(w http.ResponseWriter, payload map[string]any) (string, bool) {
	value, ok := payload["id"]
	if !ok {
		return "", true
	}
	id, ok := value.(string)
	if !ok {
		http.Error(w, "id must be a string", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func generatedManagerID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate random manager id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func writeManagerMutationError(w http.ResponseWriter, logger *zap.Logger, err error, action string, path string) {
	if errors.Is(err, appsqlite.ErrRevisionConflict) {
		logger.Debug(action+" revision conflict", zap.String("path", path), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusConflict), http.StatusConflict)
		return
	}
	logger.Error(action+" failed", zap.String("path", path), zap.Error(err))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func writeRawCreatedJSON(w http.ResponseWriter, logger *zap.Logger, value []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write(value); err != nil {
		logger.Error("write created json response failed", zap.Error(err))
	}
}

func writeCreatedJSON(w http.ResponseWriter, logger *zap.Logger, value any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logger.Error("encode created json response failed", zap.Error(err))
	}
}

func validateRequiredStringEnum(w http.ResponseWriter, payload map[string]any, key string, allowed ...string) bool {
	value, ok := payload[key]
	if !ok {
		http.Error(w, key+" is required", http.StatusBadRequest)
		return false
	}
	got, ok := value.(string)
	if !ok {
		http.Error(w, key+" must be a string", http.StatusBadRequest)
		return false
	}
	if slices.Contains(allowed, got) {
		return true
	}
	http.Error(w, key+" is not allowed", http.StatusBadRequest)
	return false
}

func validateRequiredBool(w http.ResponseWriter, payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok {
		http.Error(w, key+" is required", http.StatusBadRequest)
		return false
	}
	if _, ok = value.(bool); !ok {
		http.Error(w, key+" must be a boolean", http.StatusBadRequest)
		return false
	}
	return true
}
