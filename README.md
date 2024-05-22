### SUQS Simple Universal Queue Service 

### Narrative Explanation of the Queueing System Semantics and Workflow

#### Overview

The queueing system is a message queue service built on SQLite, providing a way to enqueue, dequeue, and manage messages across multiple named queues. It ensures message integrity, handles concurrency, and supports features like message visibility timeouts and priorities. The system also provides endpoints for managing and retrieving information about the queues.

#### Initialization

1. **Starting the Service**:
   - When the service starts, it attempts to connect to the SQLite database file specified in the configuration. If the file does not exist, SQLite creates a new database file at the specified path.

2. **Creating Tables**:
   - Upon connection, the service initializes the database by creating the necessary table (`messages`) if it does not already exist. This table stores all the messages with various attributes such as queue name, message content, priority, visibility timeout, and creation time.

#### Enqueuing Messages

**Endpoint**: `POST /enqueue`

1. **Request Structure**:
   - Clients send a request with a JSON body containing `queue_name`, `message`, and optionally `priority`.

2. **Processing**:
   - The server locks the database to ensure thread safety.
   - It then inserts the message into the specified queue with the provided attributes. The creation time is recorded in nanoseconds for high precision.

3. **Response**:
   - The server increments the enqueue counter for statistics and responds with a status indicating success or failure.

**Example**:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","message":"Hello World"}' http://localhost:8080/enqueue
```

#### Dequeuing Messages

**Endpoint**: `POST /dequeue`

1. **Request Structure**:
   - Clients send a request with a JSON body containing `queue_name`, `visibility_timeout`, and optionally `poll_interval`.

2. **Processing**:
   - The server locks the database and attempts to retrieve a message from the specified queue.
   - If a message is found, it updates the visibility timestamp to hide it for the specified timeout period and generates a unique delete token.
   - If no message is found, the server enters a long-polling mode, periodically checking for new messages until a message is found or a 30-second timeout is reached.

3. **Response**:
   - If a message is found, the server increments the dequeue counter and responds with the message content and delete token.
   - If no message is found within the timeout period, the server responds with a 204 No Content status.

**Example**:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10}' http://localhost:8080/dequeue
```

#### Deleting Messages

**Endpoint**: `POST /delete`

1. **Request Structure**:
   - Clients send a request with a JSON body containing `delete_token`.

2. **Processing**:
   - The server locks the database and attempts to delete the message associated with the provided delete token.

3. **Response**:
   - If the message is successfully deleted, the server increments the delete counter and responds with a status indicating success.
   - If the delete token is invalid, the server responds with an appropriate error message.

**Example**:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"delete_token":"<delete_token>"}' http://localhost:8080/delete
```

#### Getting Queue Length

**Endpoint**: `POST /queue_length`

1. **Request Structure**:
   - Clients send a request with a JSON body containing `queue_name`.

2. **Processing**:
   - The server locks the database and counts the number of visible, unprocessed messages in the specified queue.

3. **Response**:
   - The server increments the queue length counter and responds with the count of messages in the queue.

**Example**:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1"}' http://localhost:8080/queue_length
```

#### Getting Unique Queue Names

**Endpoint**: `GET /unique_queue_names`

1. **Processing**:
   - The server locks the database and retrieves all unique queue names along with the count of messages in each queue.

2. **Response**:
   - The server increments the unique queue names counter and responds with a list of queue names and their message counts.

**Example**:
```sh
curl -X GET http://localhost:8080/unique_queue_names
```

#### Getting Stats

**Endpoint**: `GET /stats`

1. **Processing**:
   - The server retrieves the current statistics counters for each type of request (enqueue, dequeue, delete, etc.).

2. **Response**:
   - The server responds with an HTML page displaying the statistics in a simple list format.

**Example**:
```sh
curl -X GET http://localhost:8080/stats
```

### Additional Information

**Server Configuration**:
- The server can be configured to listen on a specific port and host using command-line options `--port` and `--host`.

**Starting the Server**:
```sh
go run main.go --port 9090 --host 0.0.0.0
```

**Help and Version**:
```sh
go run main.go --version
go run main.go --help
```

### Summary

The queueing system provides a robust way to manage message queues with support for priority, visibility timeouts, and long-polling. It ensures data integrity and handles concurrency through database locks. The system also provides endpoints for monitoring and managing the queues, making it a comprehensive solution for message queue management.

### API Documentation

#### Table of Contents
- [Enqueue](#enqueue)
- [Dequeue](#dequeue)
- [Delete](#delete)
- [Get Queue Length](#get-queue-length)
- [Get Unique Queue Names](#get-unique-queue-names)
- [Get Stats](#get-stats)

---

### Enqueue

**Endpoint:** `POST /enqueue`

**Description:** Enqueues a message into the specified queue.

**Request Body:**
- `queue_name` (string, required): The name of the queue.
- `message` (string, required): The message to enqueue.
- `priority` (integer, optional): The priority of the message (higher numbers indicate higher priority).

**Curl Examples:**
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","message":"Message 1"}' http://localhost:8080/enqueue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","message":"Message 2","priority":1}' http://localhost:8080/enqueue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2","message":"Message 3","priority":2}' http://localhost:8080/enqueue
```

---

### Dequeue

**Endpoint:** `POST /dequeue`

**Description:** Dequeues a message from the specified queue. Supports long polling.

**Request Body:**
- `queue_name` (string, required): The name of the queue.
- `visibility_timeout` (integer, required): The time in seconds to hide the message from other dequeue calls.
- `poll_interval` (integer, optional): The interval in seconds to poll the database, between 1 and 5. Default is 1.

**Curl Examples:**
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10,"poll_interval":2}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2","visibility_timeout":20}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2","visibility_timeout":20,"poll_interval":3}' http://localhost:8080/dequeue
```

---

### Delete

**Endpoint:** `POST /delete`

**Description:** Deletes a message from the queue using the provided delete token.

**Request Body:**
- `delete_token` (string, required): The delete token associated with the message.

**Curl Examples:**
```sh
curl -X POST -H "Content-Type: application/json" -d '{"delete_token":"<delete_token>"}' http://localhost:8080/delete
```

---

### Get Queue Length

**Endpoint:** `POST /queue_length`

**Description:** Gets the number of messages in the specified queue.

**Request Body:**
- `queue_name` (string, required): The name of the queue.

**Curl Examples:**
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1"}' http://localhost:8080/queue_length
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2"}' http://localhost:8080/queue_length
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue3"}' http://localhost:8080/queue_length
```

---

### Get Unique Queue Names

**Endpoint:** `GET /unique_queue_names`

**Description:** Gets a list of all unique queue names and the count of messages in each queue.

**Curl Examples:**
```sh
curl -X GET http://localhost:8080/unique_queue_names
```

---

### Get Stats

**Endpoint:** `GET /stats`

**Description:** Gets statistics about the number of requests made to each endpoint.

**Curl Examples:**
```sh
curl -X GET http://localhost:8080/stats
```

---

### Additional Information

#### Starting the Server

To start the server with the default options (listening on `localhost` and port `8080`):

```sh
go run main.go
```

To specify a custom port and host:

```sh
go run main.go --port 9090 --host 0.0.0.0
```

#### Command-Line Options

- `--version`: Display the version of the application.
- `--help`: Display help message.
- `--port`: Specify the port to listen on (default: 8080).
- `--host`: Specify the host to listen on (default: localhost).

```sh
go run main.go --version
go run main.go --help
```

By using the above curl examples and command-line options, you can interact with the message queue service and perform various operations such as enqueueing, dequeueing, deleting messages, and retrieving statistics.
