package main
import (
	"database/sql"
	"fmt"
	"log"
	_ "modernc.org/sqlite"
)
func main() {
	db, err := sql.Open("sqlite", "data/cms.db")
	if err != nil { log.Fatal(err) }
	rows, err := db.Query("SELECT id, title, parent_id FROM pages")
	if err != nil { log.Fatal(err) }
	defer rows.Close()
	for rows.Next() {
		var id, title, parent sql.NullString
		rows.Scan(&id, &title, &parent)
		fmt.Printf("ID: %s, Title: %s, Parent: %s\n", id.String, title.String, parent.String)
	}
}
