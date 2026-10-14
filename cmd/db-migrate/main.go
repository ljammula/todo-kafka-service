// Command db-migrate applies or rolls back the service's database migrations.
package main

import (
	"fmt"
	"os"

	"todo-service/internal/dbmigrate"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: db-migrate up|down")
		os.Exit(2)
	}
	dsn := os.Getenv("TODO_DATABASE_URL")
	if err := dbmigrate.Apply(dsn, os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("database migrations %s complete\n", os.Args[1])
}
