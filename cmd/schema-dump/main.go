// This program applies all migrations to a temporary database and prints the
// resulting schema
package main

import (
	"context"
	"fmt"

	"github.com/bcmk/siren/v2/internal/db"
	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	checkErr = cmdlib.CheckErr
	linf     = cmdlib.Linf
)

func main() {
	ctx := context.Background()

	linf("starting PostgreSQL container...")
	pgContainer, err := postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	checkErr(err)
	defer func() { checkErr(pgContainer.Terminate(ctx)) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	checkErr(err)

	linf("applying migrations...")
	database := db.NewDatabase(connStr, false)
	defer func() { checkErr(database.Close()) }()

	database.ApplyMigrations()

	linf("dumping schema...\n")
	printSchema(database)
}

type column struct {
	name         string
	dataType     string
	isNullable   string
	defaultValue *string
}

type index struct {
	name string
	def  string
}

type check struct {
	name string
	def  string
}

type table struct {
	columns []column
	indexes []index
	checks  []check
}

func printSchema(database db.Database) {
	tables := make(map[string]*table)
	var tableOrder []string

	var tableName, columnName, dataType, isNullable string
	var columnDefault *string
	database.MustQuery(
		`
			select
				c.table_name,
				c.column_name,
				c.data_type,
				c.is_nullable,
				c.column_default
			from information_schema.columns c
			join information_schema.tables t
				on c.table_name = t.table_name
				and c.table_schema = t.table_schema
			where c.table_schema = 'public'
				and t.table_type = 'BASE TABLE'
			order by c.table_name, c.ordinal_position
		`,
		nil,
		db.ScanTo{&tableName, &columnName, &dataType, &isNullable, &columnDefault},
		func() {
			if tables[tableName] == nil {
				tables[tableName] = &table{}
				tableOrder = append(tableOrder, tableName)
			}
			tables[tableName].columns = append(tables[tableName].columns, column{
				name:         columnName,
				dataType:     dataType,
				isNullable:   isNullable,
				defaultValue: columnDefault,
			})
		})

	var indexName, indexTable, indexDef string
	database.MustQuery(
		`
			select indexname, tablename, indexdef
			from pg_indexes
			where schemaname = 'public'
			order by tablename, indexname
		`,
		nil,
		db.ScanTo{&indexName, &indexTable, &indexDef},
		func() {
			if tables[indexTable] != nil {
				tables[indexTable].indexes = append(tables[indexTable].indexes, index{
					name: indexName,
					def:  indexDef,
				})
			}
		})

	var checkName, checkTable, checkDef string
	database.MustQuery(
		`
			select conname, relname, pg_get_constraintdef(c.oid)
			from pg_constraint c
			join pg_class r on c.conrelid = r.oid
			join pg_namespace n on r.relnamespace = n.oid
			where n.nspname = 'public' and c.contype = 'c'
			order by relname, conname
		`,
		nil,
		db.ScanTo{&checkName, &checkTable, &checkDef},
		func() {
			if tables[checkTable] != nil {
				tables[checkTable].checks = append(tables[checkTable].checks, check{
					name: checkName,
					def:  checkDef,
				})
			}
		})

	for i, name := range tableOrder {
		if i > 0 {
			fmt.Println()
		}
		t := tables[name]
		fmt.Printf("table %s\n", name)

		maxName, maxType := 0, 0
		for _, col := range t.columns {
			if len(col.name) > maxName {
				maxName = len(col.name)
			}
			if len(col.dataType) > maxType {
				maxType = len(col.dataType)
			}
		}

		for _, col := range t.columns {
			nullable := ""
			if col.isNullable == "NO" {
				nullable = "not null"
			}
			def := ""
			if col.defaultValue != nil {
				def = fmt.Sprintf("default %s", *col.defaultValue)
			}
			fmt.Printf("    %-*s  %-*s  %-8s  %s\n",
				maxName, col.name,
				maxType, col.dataType,
				nullable,
				def)
		}

		if len(t.indexes) > 0 {
			fmt.Println()
			for _, idx := range t.indexes {
				fmt.Printf("    index %s: %s\n", idx.name, idx.def)
			}
		}

		if len(t.checks) > 0 {
			fmt.Println()
			for _, chk := range t.checks {
				fmt.Printf("    check %s: %s\n", chk.name, chk.def)
			}
		}
	}
}
