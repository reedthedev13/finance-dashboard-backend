package main

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gocarina/gocsv"
	_ "github.com/mattn/go-sqlite3"
)

type Transaction struct {
	ID          int       `json:"id" csv:"id"`
	Date        time.Time `json:"date" csv:"date"`
	Amount      float64   `json:"amount" csv:"amount"`
	Category    string    `json:"category" csv:"category"`
	Description string    `json:"description" csv:"description"`
	Type        string    `json:"type" csv:"type"`
}

type Budget struct {
	Category string  `json:"category" csv:"category"`
	Amount   float64 `json:"amount" csv:"amount"`
}

type MonthlySummary struct {
	Month        string  `json:"month"`
	TotalIncome  float64 `json:"total_income"`
	TotalExpense float64 `json:"total_expense"`
	Savings      float64 `json:"savings"`
}

var db *sql.DB

func main() {
	var err error
	db, err = sql.Open("sqlite3", "./finance.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	createTables()

	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	r.GET("/api/transactions", getTransactions)
	r.POST("/api/transactions", addTransaction)
	r.DELETE("/api/transactions/:id", deleteTransaction)
	r.POST("/api/transactions/import", importTransactions)
	r.GET("/api/transactions/export", exportTransactions)
	r.GET("/api/summary/monthly", getMonthlySummary)
	r.GET("/api/summary/categories", getCategorySummary)

	r.Run(":8080")
}

func createTables() {

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			date DATE NOT NULL,
			amount REAL NOT NULL,
			category TEXT NOT NULL,
			description TEXT,
			type TEXT NOT NULL
		)
	`)
	if err != nil {
		panic(err)
	}
}

func getTransactions(c *gin.Context) {
	rows, err := db.Query("SELECT id, date, amount, category, description, type FROM transactions ORDER BY date DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var t Transaction
		err := rows.Scan(&t.ID, &t.Date, &t.Amount, &t.Category, &t.Description, &t.Type)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		transactions = append(transactions, t)
	}

	c.JSON(http.StatusOK, transactions)
}

func addTransaction(c *gin.Context) {
	var t Transaction
	if err := c.ShouldBindJSON(&t); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if t.Type == "expense" && t.Amount > 0 {
		t.Amount = -t.Amount
	}

	result, err := db.Exec(
		"INSERT INTO transactions (date, amount, category, description, type) VALUES (?, ?, ?, ?, ?)",
		t.Date, t.Amount, t.Category, t.Description, t.Type,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	id, _ := result.LastInsertId()
	t.ID = int(id)
	c.JSON(http.StatusCreated, t)
}

func deleteTransaction(c *gin.Context) {
	id := c.Param("id")
	_, err := db.Exec("DELETE FROM transactions WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func importTransactions(c *gin.Context) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer file.Close()

	var transactions []*Transaction
	if err := gocsv.Unmarshal(file, &transactions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	for _, t := range transactions {
		_, err := tx.Exec(
			"INSERT INTO transactions (date, amount, category, description, type) VALUES (?, ?, ?, ?, ?)",
			t.Date, t.Amount, t.Category, t.Description, t.Type,
		)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	tx.Commit()
	c.Status(http.StatusCreated)
}

func exportTransactions(c *gin.Context) {
	rows, err := db.Query("SELECT id, date, amount, category, description, type FROM transactions")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var t Transaction
		err := rows.Scan(&t.ID, &t.Date, &t.Amount, &t.Category, &t.Description, &t.Type)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		transactions = append(transactions, t)
	}

	csvContent, err := gocsv.MarshalString(transactions)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment;filename=transactions.csv")
	c.String(http.StatusOK, csvContent)
}

func getMonthlySummary(c *gin.Context) {
	rows, err := db.Query(`
        SELECT 
            strftime('%Y-%m', date) as month,
            ROUND(SUM(CASE WHEN type = 'income' THEN amount ELSE 0 END), 2) as income,
            ROUND(SUM(CASE WHEN type = 'expense' THEN ABS(amount) ELSE 0 END), 2) as expense
        FROM transactions
        GROUP BY strftime('%Y-%m', date)
        ORDER BY month DESC
        LIMIT 12
    `)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var summaries []MonthlySummary
	for rows.Next() {
		var s MonthlySummary
		var income, expense float64
		err := rows.Scan(&s.Month, &income, &expense)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		s.TotalIncome = income
		s.TotalExpense = expense
		s.Savings = income - expense
		summaries = append(summaries, s)
	}

	c.JSON(http.StatusOK, summaries)
}

func getCategorySummary(c *gin.Context) {
	rows, err := db.Query(`
		SELECT 
			category,
			SUM(amount) as total,
			type
		FROM transactions
		GROUP BY category, type
		ORDER BY type, total DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type CategorySummary struct {
		Category string  `json:"category"`
		Total    float64 `json:"total"`
		Type     string  `json:"type"`
	}

	var summaries []CategorySummary
	for rows.Next() {
		var s CategorySummary
		err := rows.Scan(&s.Category, &s.Total, &s.Type)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		summaries = append(summaries, s)
	}

	c.JSON(http.StatusOK, summaries)
}
