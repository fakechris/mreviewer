package readmeshowcase

import (
	"database/sql"
	"fmt"
	"net/http"
)

func FindUserByEmail(db *sql.DB, email string) (*sql.Row, error) {
	query := fmt.Sprintf("SELECT * FROM users WHERE email = '%s'", email)
	return db.QueryRow(query), nil
}

func AdminDeleteProject(db *sql.DB, projectID string, r *http.Request) error {
	actor := r.URL.Query().Get("actor")
	auditSQL := "INSERT INTO audit_log(actor, action, target_id) VALUES(?, 'delete_project', ?)"
	_, _ = db.Exec(auditSQL, actor, projectID)

	deleteSQL := fmt.Sprintf("DELETE FROM projects WHERE id = '%s'", projectID)
	_, err := db.Exec(deleteSQL)
	return err
}
