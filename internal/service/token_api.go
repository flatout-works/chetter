package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
)

func (s *Service) TokenAPIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/v1/tokens", s.handleCreateToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{name}", s.handleDeleteToken)
	return mux
}

func (s *Service) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rows, err := s.repo.ListTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tokens := make([]map[string]any, len(rows))
	for i, r := range rows {
		tokens[i] = map[string]any{
			"name":       r.Name,
			"user_name":  r.UserName,
			"team_name":  r.TeamName,
			"created_at": r.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Service) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req struct {
		TeamName  string `json:"team_name"`
		UserName  string `json:"user_name"`
		TokenName string `json:"token_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.TeamName == "" || req.UserName == "" || req.TokenName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "team_name, user_name, and token_name are required"})
		return
	}
	now := time.Now().UTC()
	ctx := r.Context()

	team, err := s.repo.GetTeamByName(ctx, req.TeamName)
	if err != nil {
		teamID, err := randomID("team")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.repo.CreateTeam(ctx, repository.CreateTeamParams{
			ID:        teamID,
			Name:      req.TeamName,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		team.ID = teamID
		team.Name = req.TeamName
	}

	userID, err := randomID("user")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.repo.CreateUser(ctx, repository.CreateUserParams{
		ID:        userID,
		Name:      req.UserName,
		TeamID:    team.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rawToken, err := randomID("chtr")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hash := sha256.Sum256([]byte(rawToken))
	tokenID, err := randomID("tok")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.repo.CreateToken(ctx, repository.CreateTokenParams{
		ID:        tokenID,
		Name:      req.TokenName,
		TokenHash: hex.EncodeToString(hash[:]),
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     rawToken,
		"team_id":   team.ID,
		"team_name": team.Name,
		"user_id":   userID,
		"user_name": req.UserName,
	})
}

func (s *Service) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if err := s.repo.DeleteToken(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	scope, ok := auth.GetScope(r.Context())
	if !ok || !scope.Admin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
