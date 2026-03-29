package session

import (
	"database/sql"
	"fmt"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"
)

// RoleTemplate represents a reusable agent role template.
type RoleTemplate struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	SystemPrompt string       `json:"systemPrompt"`
	Provider     ProviderName `json:"provider"`
	Model        string       `json:"model,omitempty"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}

// TemplateStore provides CRUD operations for role templates.
type TemplateStore struct {
	db *sql.DB
}

// NewTemplateStore creates a new TemplateStore.
func NewTemplateStore(db *sql.DB) *TemplateStore {
	return &TemplateStore{db: db}
}

func newTemplateID() (string, error) {
	return gonanoid.Generate(nanoidAlphabet, 5)
}

// List returns all role templates ordered by name.
func (s *TemplateStore) List() ([]RoleTemplate, error) {
	rows, err := s.db.Query(`SELECT id, name, system_prompt, provider, model, created_at, updated_at FROM role_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()

	var templates []RoleTemplate
	for rows.Next() {
		var t RoleTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.SystemPrompt, &t.Provider, &t.Model, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

// Get returns a template by ID.
func (s *TemplateStore) Get(id string) (*RoleTemplate, error) {
	var t RoleTemplate
	err := s.db.QueryRow(`SELECT id, name, system_prompt, provider, model, created_at, updated_at FROM role_templates WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.SystemPrompt, &t.Provider, &t.Model, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get template %s: %w", id, err)
	}
	return &t, nil
}

// GetByName returns a template by name.
func (s *TemplateStore) GetByName(name string) (*RoleTemplate, error) {
	var t RoleTemplate
	err := s.db.QueryRow(`SELECT id, name, system_prompt, provider, model, created_at, updated_at FROM role_templates WHERE name = ?`, name).
		Scan(&t.ID, &t.Name, &t.SystemPrompt, &t.Provider, &t.Model, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get template by name %s: %w", name, err)
	}
	return &t, nil
}

// Create creates a new role template.
func (s *TemplateStore) Create(name, systemPrompt string, provider ProviderName, model string) (*RoleTemplate, error) {
	id, err := newTemplateID()
	if err != nil {
		return nil, fmt.Errorf("generate template id: %w", err)
	}
	if provider == "" {
		provider = ProviderClaude
	}

	now := time.Now()
	_, err = s.db.Exec(
		`INSERT INTO role_templates (id, name, system_prompt, provider, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, systemPrompt, string(provider), model, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}

	return &RoleTemplate{
		ID:           id,
		Name:         name,
		SystemPrompt: systemPrompt,
		Provider:     provider,
		Model:        model,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Update updates an existing role template.
func (s *TemplateStore) Update(id, name, systemPrompt string, provider ProviderName, model string) (*RoleTemplate, error) {
	now := time.Now()
	result, err := s.db.Exec(
		`UPDATE role_templates SET name = ?, system_prompt = ?, provider = ?, model = ?, updated_at = ? WHERE id = ?`,
		name, systemPrompt, string(provider), model, now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update template: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("template %s not found", id)
	}
	return s.Get(id)
}

// Delete deletes a role template by ID.
func (s *TemplateStore) Delete(id string) error {
	result, err := s.db.Exec(`DELETE FROM role_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("template %s not found", id)
	}
	return nil
}
