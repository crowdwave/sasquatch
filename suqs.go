package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const version = "1.0.0"

type MessageQueue struct {
	db   *sql.DB
	lock sync.Mutex
}

type Stats struct {
	EnqueueCount           int
	DequeueCount           int
	DeleteCount            int
	GetQueueLengthCount    int
	GetUniqueQueueNamesCount int
}

type EnqueueRequest struct {
	QueueName string `json:"queue_name" validate:"required"`
	Message   string `json:"message" validate:"required"`
	Priority  int    `json:"priority"`
}

type DequeueRequest struct {
	QueueName         string `json:"queue_name" validate:"required"`
	VisibilityTimeout int    `json:"visibility_timeout" validate:"required,min=1"`
	PollInterval      int    `json:"poll_interval" validate:"omitempty,min=1,max=5"`
}

type DeleteRequest struct {
	DeleteToken string `json:"delete_token" validate:"required,uuid4"`
}

type QueueLengthRequest struct {
	QueueName string `json:"queue_name" validate:"required"`
}

type QueueLengthResponse struct {
	QueueName string `json:"queue_name"`
	Count     int    `json:"count"`
}

type UniqueQueueNamesResponse struct {
	QueueName string `json:"queue_name"`
	Count     int    `json:"count"`
}

var validate *validator.Validate
var stats Stats
var statsLock sync.Mutex

func NewMessageQueue(dbFilePath string) (*MessageQueue, error) {
	db, err := sql.Open("sqlite3", dbFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	mq := &MessageQueue{db: db}
	if err := mq.initialize(); err != nil {
		return nil, err
	}

	return mq, nil
}

func (mq *MessageQueue) initialize() error {
	createTableQuery := `
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			queue_name TEXT NOT NULL,
			message TEXT NOT NULL,
			processed INTEGER DEFAULT 0,
			visibility_timestamp INTEGER DEFAULT 0,
			delete_token TEXT,
			read_attempts INTEGER DEFAULT 0,
			priority INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		)
	`
	_, err := mq.db.Exec(createTableQuery)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}
	return nil
}

func (mq *MessageQueue) Enqueue(queueName, message string, priority int) error {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	createdAt := time.Now().UnixNano()
	stmt, err := mq.db.Prepare("INSERT INTO messages (queue_name, message, priority, created_at) VALUES (?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare enqueue statement: %w", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(queueName, message, priority, createdAt)
	if err != nil {
		return fmt.Errorf("failed to execute enqueue statement: %w", err)
	}
	return nil
}

func (mq *MessageQueue) Dequeue(queueName string, visibilityTimeout, pollInterval int) (string, string, error) {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	currentTime := time.Now().Unix()
	selectStmt := `
		SELECT id, message FROM messages
		WHERE queue_name = ? AND processed = 0 AND visibility_timestamp <= ?
		ORDER BY priority DESC, created_at DESC, id DESC LIMIT 1
	`
	updateStmt := `
		UPDATE messages
		SET visibility_timestamp = ?, delete_token = ?, read_attempts = read_attempts + 1
		WHERE id = ?
	`

	tx, err := mq.db.Begin()
	if err != nil {
		return "", "", fmt.Errorf("failed to begin transaction: %w", err)
	}

	var id int
	var message string
	err = tx.QueryRow(selectStmt, queueName, currentTime).Scan(&id, &message)
	if err != nil {
		tx.Rollback()
		if err == sql.ErrNoRows {
			return "", "", nil
		}
		return "", "", fmt.Errorf("failed to select message: %w", err)
	}

	newVisibilityTimestamp := currentTime + int64(visibilityTimeout)
	deleteToken := uuid.New().String()
	_, err = tx.Exec(updateStmt, newVisibilityTimestamp, deleteToken, id)
	if err != nil {
		tx.Rollback()
		return "", "", fmt.Errorf("failed to update message: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return "", "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	return message, deleteToken, nil
}

func (mq *MessageQueue) DeleteMessage(deleteToken string) (bool, error) {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	deleteStmt := "DELETE FROM messages WHERE delete_token = ?"

	tx, err := mq.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}

	result, err := tx.Exec(deleteStmt, deleteToken)
	if err != nil {
		tx.Rollback()
		return false, fmt.Errorf("failed to execute delete statement: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return false, fmt.Errorf("failed to commit transaction: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to retrieve rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

func (mq *MessageQueue) GetQueueLength(queueName string) (int, error) {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	currentTime := time.Now().Unix()
	stmt := "SELECT COUNT(*) AS count FROM messages WHERE queue_name = ? AND processed = 0 AND visibility_timestamp <= ?"
	row := mq.db.QueryRow(stmt, queueName, currentTime)

	var count int
	err := row.Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to scan queue length: %w", err)
	}
	return count, nil
}

func (mq *MessageQueue) GetUniqueQueueNames() ([]UniqueQueueNamesResponse, error) {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	currentTime := time.Now().Unix()
	stmt := `
		SELECT queue_name, COUNT(*) AS count
		FROM messages
		WHERE processed = 0 AND visibility_timestamp <= ?
		GROUP BY queue_name
	`

	rows, err := mq.db.Query(stmt, currentTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query unique queue names: %w", err)
	}
	defer rows.Close()

	var result []UniqueQueueNamesResponse

	for rows.Next() {
		var queueName string
		var count int
		if err := rows.Scan(&queueName, &count); err != nil {
			return nil, fmt.Errorf("failed to scan queue name and count: %w", err)
		}
		result = append(result, UniqueQueueNamesResponse{QueueName: queueName, Count: count})
	}

	return result, nil
}

func incrementStatsCounter(counter *int) {
	statsLock.Lock()
	defer statsLock.Unlock()
	*counter++
}

func enqueueHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req EnqueueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := validate.Struct(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := mq.Enqueue(req.QueueName, req.Message, req.Priority); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		incrementStatsCounter(&stats.EnqueueCount)
		w.WriteHeader(http.StatusOK)
	}
}

func dequeueHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DequeueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := validate.Struct(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pollInterval := req.PollInterval
		if pollInterval == 0 {
			pollInterval = 1
		}

		timeout := time.After(30 * time.Second)
		ticker := time.NewTicker(time.Duration(pollInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				w.WriteHeader(http.StatusNoContent)
				return
			case <-ticker.C:
				message, deleteToken, err := mq.Dequeue(req.QueueName, req.VisibilityTimeout, pollInterval)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				if message != "" {
					incrementStatsCounter(&stats.DequeueCount)
					response := map[string]string{"message": message, "delete_token": deleteToken}
					json.NewEncoder(w).Encode(response)
					return
				}
			}
		}
	}
}

func deleteHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := validate.Struct(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		success, err := mq.DeleteMessage(req.DeleteToken)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !success {
			http.Error(w, "Delete failed", http.StatusNotFound)
			return
		}

		incrementStatsCounter(&stats.DeleteCount)
		w.WriteHeader(http.StatusOK)
	}
}

func getQueueLengthHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req QueueLengthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := validate.Struct(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		count, err := mq.GetQueueLength(req.QueueName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		incrementStatsCounter(&stats.GetQueueLengthCount)
		response := QueueLengthResponse{QueueName: req.QueueName, Count: count}
		json.NewEncoder(w).Encode(response)
	}
}

func getUniqueQueueNamesHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queueNames, err := mq.GetUniqueQueueNames()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		incrementStatsCounter(&stats.GetUniqueQueueNamesCount)
		json.NewEncoder(w).Encode(queueNames)
	}
}

func statsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statsLock.Lock()
		defer statsLock.Unlock()

		tmpl := `
		<html>
		<head><title>Stats</title></head>
		<body>
		<h1>Stats</h1>
		<ul>
			<li>Enqueue Count: {{.EnqueueCount}}</li>
			<li>Dequeue Count: {{.DequeueCount}}</li>
			<li>Delete Count: {{.DeleteCount}}</li>
			<li>Get Queue Length Count: {{.GetQueueLengthCount}}</li>
			<li>Get Unique Queue Names Count: {{.GetUniqueQueueNamesCount}}</li>
		</ul>
		</body>
		</html>
		`

		t, err := template.New("stats").Parse(tmpl)
		if err != nil {
			http.Error(w, "Failed to generate stats page", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		if err := t.Execute(w, stats); err != nil {
			http.Error(w, "Failed to render stats page", http.StatusInternalServerError)
		}
	}
}

func printHelp() {
	fmt.Println("Message Queue Service")
	fmt.Println("Usage:")
	fmt.Println("  --version       Display the version of the application")
	fmt.Println("  --help          Display this help message")
	fmt.Println("  --port          Specify the port to listen on (default: 8080)")
	fmt.Println("  --host          Specify the host to listen on (default: localhost)")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  POST /enqueue             Enqueue a message")
	fmt.Println("  POST /dequeue             Dequeue a message with optional poll interval")
	fmt.Println("  POST /delete              Delete a message using delete token")
	fmt.Println("  POST /queue_length        Get the length of a specific queue")
	fmt.Println("  GET  /unique_queue_names  Get unique queue names and their counts")
	fmt.Println("  GET  /stats               Display statistics about the requests")
}

func main() {
	versionFlag := flag.Bool("version", false, "Display the version of the application")
	helpFlag := flag.Bool("help", false, "Display help message")
	port := flag.String("port", "8080", "Specify the port to listen on")
	host := flag.String("host", "localhost", "Specify the host to listen on")
	flag.Parse()

	if *versionFlag {
		fmt.Println("Message Queue Service, Version:", version)
		return
	}

	if *helpFlag {
		printHelp()
		return
	}

	validate = validator.New()
	queue, err := NewMessageQueue("messageQueue.db")
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/enqueue", enqueueHandler(queue))
	http.HandleFunc("/dequeue", dequeueHandler(queue))
	http.HandleFunc("/delete", deleteHandler(queue))
	http.HandleFunc("/queue_length", getQueueLengthHandler(queue))
	http.HandleFunc("/unique_queue_names", getUniqueQueueNamesHandler(queue))
	http.HandleFunc("/stats", statsHandler())

	address := fmt.Sprintf("%s:%s", *host, *port)
	log.Printf("Server started at %s\n", address)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Fatal(err)
	}
}
