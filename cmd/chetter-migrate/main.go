package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	dir := flag.String("dir", "", "migration directory")
	driver := flag.String("driver", "", "database driver: mysql, tidb, or postgres")
	dsn := flag.String("dsn", os.Getenv("DATABASE_DSN"), "database DSN")
	flag.Parse()

	if *dir == "" || *driver == "" || *dsn == "" {
		log.Fatal("-dir, -driver, and -dsn are required")
	}

	dbDriver := *driver
	gooseDialect := *driver
	if *driver == "tidb" {
		dbDriver = "mysql"
		gooseDialect = "mysql"
	}
	if dbDriver != "mysql" && dbDriver != "postgres" {
		log.Fatalf("unsupported migration driver %q", *driver)
	}

	databaseDSN := strings.ReplaceAll(*dsn, "tls=tidb", "tls=true")
	if err := goose.SetDialect(gooseDialect); err != nil {
		log.Fatalf("set goose dialect: %v", err)
	}
	db, err := sql.Open(dbDriver, databaseDSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}
	if err := goose.UpContext(ctx, db, *dir); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	fmt.Println("database migrations applied")
}
