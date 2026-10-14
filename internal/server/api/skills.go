package api

import (
	"net/http"

	"github.com/LackOfMorals/charta-query/internal/skills"
)

// skillList answers GET /skills with the available skills and where to
// fetch each. Like discovery it needs no authentication: a skill is
// documentation and holds no secrets.
func (h *Handler) skillList(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	type entry struct {
		skills.Skill
		URL string `json:"url"`
	}
	list := skills.List()
	out := make([]entry, len(list))
	for i, s := range list {
		out[i] = entry{Skill: s, URL: base + "/skills/" + s.Name}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// skillGet answers GET /skills/{name} with the skill as Markdown, its
// examples pointing at this server.
func (h *Handler) skillGet(w http.ResponseWriter, r *http.Request) {
	doc, ok := skills.Render(r.PathValue("name"), h.baseURL(r))
	if !ok {
		notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(doc))
}
