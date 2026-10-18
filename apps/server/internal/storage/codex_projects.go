package storage

import "context"

type CodexProject struct {
	Path       string `json:"path"`
	LastUsedAt string `json:"lastUsedAt"`
}

func (s *Store) CodexProjects(ctx context.Context) ([]CodexProject, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path,last_used_at FROM codex_projects ORDER BY last_used_at DESC,path LIMIT 10")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []CodexProject{}
	for rows.Next() {
		var project CodexProject
		if err := rows.Scan(&project.Path, &project.LastUsedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}
