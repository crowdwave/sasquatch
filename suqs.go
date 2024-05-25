package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const version = "1.0.0"
const defaultVisibilityTimeout = 30
const maxVisibilityTimeout = 43200
const maxReceives = 4                  // Define maximum receive count
const cleanupInterval = 1 * time.Minute // Interval for running the cleanup task

type MessageQueue struct {
	db            *sql.DB
	lock          sync.Mutex
	cond          *sync.Cond
	maxQueueLength int
}

type Stats struct {
	EnqueueCount             int
	DequeueCount             int
	DeleteCount              int
	GetQueueLengthCount      int
	GetUniqueQueueNamesCount int
}

type EnqueueRequest struct {
	QueueName string `json:"queue_name" validate:"required,queue_name"`
	Message   string `json:"message" validate:"required"`
	Priority  int    `json:"priority"`
}

type DequeueRequest struct {
	QueueName            string `json:"queue_name" validate:"required,queue_name"`
	VisibilityTimeout    int    `json:"visibility_timeout" validate:"omitempty"`
	DatabasePollInterval int    `json:"database_poll_interval" validate:"omitempty,min=1,max=5"`
}

type DeleteRequest struct {
	DeleteToken string `json:"delete_token" validate:"required,uuid4"`
}

type QueueLengthRequest struct {
	QueueName string `json:"queue_name" validate:"required,queue_name"`
}

type QueueLengthResponse struct {
	QueueName string `json:"queue_name"`
	Count     int    `json:"count"`
}

type UniqueQueueNamesResponse struct {
	QueueName string `json:"queue_name"`
	Count     int    `json:"count"`
}

type DeleteAllRequest struct {
	QueueName string `json:"queue_name" validate:"required,queue_name"`
}

var validate *validator.Validate
var stats Stats
var statsLock sync.Mutex

func NewMessageQueue(dbFilePath string, maxQueueLength int) (*MessageQueue, error) {
	db, err := sql.Open("sqlite3", dbFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	mq := &MessageQueue{db: db, maxQueueLength: maxQueueLength}
	mq.cond = sync.NewCond(&mq.lock)
	if err := mq.initialize(); err != nil {
		return nil, err
	}

	// Start periodic cleanup task
	go mq.startCleanupTask()

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
			receive_count INTEGER DEFAULT 0,
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

func (mq *MessageQueue) startCleanupTask() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		<-ticker.C
		mq.cleanupOldMessages()
	}
}

func (mq *MessageQueue) cleanupOldMessages() {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	deleteStmt := `
		DELETE FROM messages
		WHERE receive_count > ?
	`
	_, err := mq.db.Exec(deleteStmt, maxReceives)
	if err != nil {
		log.Printf("Failed to cleanup old messages: %v", err)
	}
}

func (mq *MessageQueue) Enqueue(queueName, message string, priority int) error {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	// Check current queue length
	count, err := mq.getQueueLength(queueName)
	if err != nil {
		return fmt.Errorf("failed to get queue length: %w", err)
	}

	if count >= mq.maxQueueLength {
		return fmt.Errorf("queue %s is full", queueName)
	}

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

	mq.cond.Broadcast() // Signal waiting dequeue requests
	return nil
}

func (mq *MessageQueue) getQueueLength(queueName string) (int, error) {
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

func (mq *MessageQueue) Dequeue(queueName string, visibilityTimeout, databasePollInterval int) (string, string, error) {
	// Preliminary check without locking
	currentTime := time.Now().Unix()
	selectStmt := `
		SELECT id, message, receive_count FROM messages
		WHERE queue_name = ? AND processed = 0 AND visibility_timestamp <= ?
		ORDER BY priority DESC, created_at DESC, id DESC LIMIT 1
	`
	var id int
	var message string
	var receiveCount int
	err := mq.db.QueryRow(selectStmt, queueName, currentTime).Scan(&id, &message, &receiveCount)
	if err != nil && err != sql.ErrNoRows {
		return "", "", fmt.Errorf("failed to preliminarily select message: %w", err)
	}
	if err == sql.ErrNoRows {
		// No message available, return immediately
		return "", "", nil
	}

	// Locking section for the actual dequeue operation
	mq.lock.Lock()
	defer mq.lock.Unlock()

	if visibilityTimeout == 0 {
		visibilityTimeout = defaultVisibilityTimeout // Default visibility timeout if not provided
	} else if visibilityTimeout > maxVisibilityTimeout {
		visibilityTimeout = maxVisibilityTimeout // Cap visibility timeout at 12 hours
	} else if visibilityTimeout < 0 {
		visibilityTimeout = 0 // Minimum visibility timeout is 0 seconds
	}

	updateStmt := `
		UPDATE messages
		SET visibility_timestamp = ?, delete_token = ?, receive_count = receive_count + 1
		WHERE id = ?
	`
	for {
		tx, err := mq.db.Begin()
		if err != nil {
			return "", "", fmt.Errorf("failed to begin transaction: %w", err)
		}

		err = tx.QueryRow(selectStmt, queueName, currentTime).Scan(&id, &message, &receiveCount)
		if err != nil {
			tx.Rollback()
			if err == sql.ErrNoRows {
				mq.cond.Wait() // Wait for signal from enqueue
				continue
			}
			return "", "", fmt.Errorf("failed to select message: %w", err)
		}

		// Check if the message has exceeded the max receive count
		if receiveCount >= maxReceives {
			// Handle the poison message (delete or move to special queue)
			deleteStmt := `DELETE FROM messages WHERE id = ?`
			_, err := tx.Exec(deleteStmt, id)
			if err != nil {
				tx.Rollback()
				return "", "", fmt.Errorf("failed to delete poison message: %w", err)
			}
			err = tx.Commit()
			if err != nil {
				return "", "", fmt.Errorf("failed to commit transaction: %w", err)
			}
			mq.cond.Broadcast()
			continue // Retry the loop to get the next message
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

func (mq *MessageQueue) DeleteAllMessages(queueName string) error {
	mq.lock.Lock()
	defer mq.lock.Unlock()

	var deleteStmt string
	if queueName == "*" {
		deleteStmt = "DELETE FROM messages"
	} else {
		deleteStmt = "DELETE FROM messages WHERE queue_name = ?"
	}

	tx, err := mq.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if queueName == "*" {
		_, err = tx.Exec(deleteStmt)
	} else {
		_, err = tx.Exec(deleteStmt, queueName)
	}

	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to execute delete statement: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
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

		databasePollInterval := req.DatabasePollInterval
		if databasePollInterval == 0 {
			databasePollInterval = 1
		}

		timeout := time.After(30 * time.Second)
		ticker := time.NewTicker(time.Duration(databasePollInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				w.WriteHeader(http.StatusNoContent)
				return
			case <-ticker.C:
				message, deleteToken, err := mq.Dequeue(req.QueueName, req.VisibilityTimeout, databasePollInterval)
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

func deleteAllHandler(mq *MessageQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DeleteAllRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := validate.Struct(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err := mq.DeleteAllMessages(req.QueueName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

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
	fmt.Println("  --memory        Use in-memory database")
	fmt.Println("  --max-queue-length  Specify the maximum queue length (default: 5000)")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  POST /enqueue             Enqueue a message")
	fmt.Println("  POST /dequeue             Dequeue a message with optional database poll interval")
	fmt.Println("  POST /delete              Delete a message using delete token")
	fmt.Println("  POST /delete_all          Delete all messages in a specified queue or all messages in the database")
	fmt.Println("  POST /queue_length        Get the length of a specific queue")
	fmt.Println("  GET  /queues              Get unique queue names and their counts")
	fmt.Println("  GET  /stats               Display statistics about the requests")
}

func main() {
	versionFlag := flag.Bool("version", false, "Display the version of the application")
	helpFlag := flag.Bool("help", false, "Display help message")
	port := flag.String("port", "8080", "Specify the port to listen on")
	host := flag.String("host", "localhost", "Specify the host to listen on")
	memory := flag.Bool("memory", false, "Use in-memory database")
	maxQueueLength := flag.Int("max-queue-length", 5000, "Specify the maximum queue length")
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
	validate.RegisterValidation("queue_name", func(fl validator.FieldLevel) bool {
		re := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
		return re.MatchString(fl.Field().String())
	})

	dbFilePath := "messageQueue.db"
	if *memory {
		dbFilePath = ":memory:"
	}

	queue, err := NewMessageQueue(dbFilePath, *maxQueueLength)
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/enqueue", enqueueHandler(queue))
	http.HandleFunc("/dequeue", dequeueHandler(queue))
	http.HandleFunc("/delete", deleteHandler(queue))
	http.HandleFunc("/delete_all", deleteAllHandler(queue))
	http.HandleFunc("/queue_length", getQueueLengthHandler(queue))
	http.HandleFunc("/queues", getUniqueQueueNamesHandler(queue))
	http.HandleFunc("/stats", statsHandler())

	address := fmt.Sprintf("%s:%s", *host, *port)
	log.Printf("Server started at %s\n", address)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Fatal(err)
	}
}
