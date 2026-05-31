package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/yereke99/stone/internal/bot"
)

func main() {
	csvPath := flag.String("csv", "imports/historical_closed_contacts.csv", "path to exported historical WhatsApp contacts CSV")
	dbPath := flag.String("db", envOrDefault("DATABASE_PATH", "./data/stone.sqlite3"), "path to SQLite database")
	dryRun := flag.Bool("dry-run", false, "parse and report without writing to the database")
	flag.Parse()

	parseResult, err := bot.LoadHistoricalClosedContactsCSV(*csvPath)
	if err != nil {
		log.Fatalf("read contacts csv: %v", err)
	}

	fmt.Printf("historical contacts source: %s\n", bot.HistoricalClosedImportSource)
	fmt.Printf("csv rows: %d\n", parseResult.TotalRows)
	fmt.Printf("unique valid contacts: %d\n", len(parseResult.Contacts))
	fmt.Printf("invalid rows: %d\n", len(parseResult.Invalid))
	fmt.Printf("duplicates: %d\n", len(parseResult.Duplicates))
	for _, invalid := range parseResult.Invalid {
		fmt.Printf("invalid row %d phone=%q reason=%s\n", invalid.Row, invalid.RawPhone, invalid.Reason)
	}
	for _, duplicate := range parseResult.Duplicates {
		fmt.Printf("duplicate row %d repeats row %d chat_id=%s raw=%q\n", duplicate.Row, duplicate.FirstRow, duplicate.ChatID, duplicate.RawPhone)
	}
	if *dryRun {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := bot.NewSQLiteConversationStore(ctx, *dbPath)
	if err != nil {
		log.Fatalf("open sqlite store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close sqlite store: %v", err)
		}
	}()

	summary, err := store.ImportHistoricalClosedContacts(ctx, parseResult.Contacts, bot.HistoricalClosedImportSource)
	if err != nil {
		log.Fatalf("import historical contacts: %v", err)
	}
	summary.Invalid = parseResult.Invalid
	summary.Duplicates = parseResult.Duplicates

	fmt.Printf("inserted: %d\n", summary.Inserted)
	fmt.Printf("updated: %d\n", summary.Updated)
	fmt.Printf("skipped active/newer: %d\n", summary.SkippedActive)
	fmt.Printf("preserved opt-out: %d\n", summary.PreservedOptOut)
	fmt.Println("no WhatsApp messages or manager notifications are sent by this import command")
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
