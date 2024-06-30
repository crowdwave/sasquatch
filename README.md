### Sasquatch Zero Config Message Queue Server

### IMPORTANT! PROJECT STATUS! THIS IS EARLY CODE AND LIKELY DOES NOT EVEN COMPILE
### ITS TOO EARLY TO DO ANYTHING USEFUL


### Narrative Explanation of the Queueing System Semantics and Workflow

#### Overview

The queueing system is a message queue service built on SQLite, providing a way to enqueue, dequeue, and manage messages across multiple named queues. It ensures message integrity, handles concurrency, and supports features like message visibility timeouts and priorities. The system also provides endpoints for managing and retrieving information about the queues.

The functionality and behaviour of Sasquatch is almost the same as Amazon SQS.

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
   - After a successful enqueue, the server broadcasts a signal to any waiting dequeue requests to check the database immediately.

3. **Response**:
   - The server increments the enqueue counter for statistics and responds with a status indicating success or failure.

**Example**:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","message":"Hello World"}' http://localhost:8080/enqueue
```

#### Dequeuing Messages

**Endpoint**: `POST /dequeue`

1. **Request Structure**:
   - Clients send a request with a JSON body containing `queue_name`, `visibility_timeout`, and optionally `database_poll_interval`.

2. **Processing**:
   - The server locks the database and attempts to retrieve a message from the specified queue.
   - If a message is found, it updates the visibility timestamp to hide it for the specified timeout period (default: 30 seconds, min: 0 seconds, max: 12 hours) and generates a unique delete token.
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

**Endpoint**: `GET /queue_names`

1. **Processing**:
   - The server locks the database and retrieves all unique queue names along with the count of messages in each queue.

2. **Response**:
   - The server increments the unique queue names counter and responds with a list of queue names and their message counts.

**Example**:
```sh
curl -X GET http://localhost:8080/queue_names
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

### Expanded Narrative Explanation of Long Polling Behavior in Dequeuing

#### Overview

The queueing system includes a sophisticated long polling mechanism for the dequeue operation. This feature ensures that clients can retrieve messages as soon as they become available, without the need for constant polling, which can be inefficient and resource-intensive.

### Long Polling Behavior in Dequeueing

**Endpoint**: `POST /dequeue`

#### Request Structure
- `queue_name` (string, required): The name of the queue from which to dequeue the message.
- `visibility_timeout` (integer, optional): The time in seconds during which the dequeued message will be hidden from other dequeue calls. Defaults to 30 seconds, with a minimum of 0 seconds and a maximum of 12 hours (43200 seconds).
- `database_poll_interval` (integer, optional): The interval in seconds at which to poll the database for new messages. Must be between 1 and 5 seconds. Defaults to 1 second if not specified.

#### Dequeue Workflow with Long Polling

1. **Initial Dequeue Attempt**:
   - When a dequeue request is received, the server locks the database to ensure thread safety and attempts to fetch a message from the specified queue.
   - The server uses a SQL query to select a message that is unprocessed and currently visible (i.e., the current time is greater than the message's visibility timestamp).
   - If a message is found, it updates the message’s visibility timestamp to the current time plus the specified `visibility_timeout`, ensuring the message is hidden from other consumers for the specified duration.
   - A unique `delete_token` is generated for the message, allowing the client to delete it later.

2. **Immediate Response**:
   - If a message is successfully dequeued in the initial attempt, the server increments the dequeue counter and immediately responds with the message content and delete token in JSON format.

3. **Entering Long Polling Mode**:
   - If no message is found in the initial attempt, the server enters long polling mode. 
   - A timeout of 30 seconds is set to ensure that the server does not wait indefinitely.

4. **Polling Loop**:
   - The server sets up a ticker based on the `database_poll_interval` (defaulting to 1 second if not specified).
   - The server then enters a loop where it periodically re-attempts to dequeue a message at each tick of the ticker.

5. **Repeated Dequeue Attempts**:
   - At each tick (determined by `database_poll_interval`), the server locks the database again and retries the dequeue operation.
   - This involves executing the same SQL query to find an unprocessed and currently visible message.

6. **Successful Dequeue During Polling**:
   - If a message is found during any of these polling attempts, the server updates its visibility timestamp, generates a delete token, and responds immediately with the message content and delete token.
   - The server also increments the dequeue counter.

7. **Timeout Handling**:
   - If no message is found after 30 seconds of polling, the server exits the loop and responds with an HTTP 204 No Content status, indicating that no message is available.

#### Example Long Polling Dequeue Request

```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10,"database_poll_interval":2}' http://localhost:8080/dequeue
```

#### Detailed Long Polling Flow

1. **Client Sends Dequeue Request**:
   - The client sends a POST request to `/dequeue` with the specified queue name, visibility timeout, and optional database poll interval.

2. **Server Attempts Immediate Dequeue**:
   - The server attempts to fetch a message from the specified queue. If successful, it returns the message and delete token immediately.

3. **Server Enters Polling Mode**:
   - If no message is found, the server sets a 30-second timeout and begins polling the database at the specified interval.

4. **Polling Attempts**:
   - The server repeatedly attempts to dequeue a message at each poll interval, locking the database for each attempt to ensure consistency.

5. **Message Found**:
   - If a message is found during polling, the server updates the message's visibility timestamp and generates a delete token, then responds immediately with the message and token.

6. **Timeout Expiry**:
   - If no message is found within 30 seconds, the server responds with HTTP 204 No Content.

### Advantages of Long Polling

1.

 **Reduced Resource Usage**:
   - Long polling reduces the need for clients to repeatedly send requests, thus conserving network and server resources.

2. **Real-Time Updates**:
   - Clients can receive messages as soon as they become available, providing near real-time updates without the need for constant polling.

3. **Efficient Waiting**:
   - The server efficiently manages the wait time by periodically checking for new messages, ensuring that clients do not experience unnecessary delays.

### Summary

The long polling mechanism in the dequeue operation ensures efficient and timely retrieval of messages from the queue. It balances the need for real-time updates with the efficient use of resources by allowing clients to wait for messages without constantly sending requests. The 30-second timeout ensures that clients are not left waiting indefinitely, while the database poll interval allows for configurable and controlled polling behavior. This design makes the message queue system robust and responsive, suitable for a variety of applications requiring reliable message handling and delivery.

### API Reference

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
- `visibility_timeout` (integer, optional): The time in seconds to hide the message from other dequeue calls. Defaults to 30 seconds, with a minimum of 0 seconds and a maximum of 12 hours (43200 seconds).
- `database_poll_interval` (integer, optional): The interval in seconds to poll the database, between 1 and 5. Default is 1.

**Curl Examples:**
```sh
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue1","visibility_timeout":10,"database_poll_interval":2}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2","visibility_timeout":20}' http://localhost:8080/dequeue
curl -X POST -H "Content-Type: application/json" -d '{"queue_name":"queue2","visibility_timeout":20,"database_poll_interval":3}' http://localhost:8080/dequeue
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

**Endpoint:** `GET /queue_names`

**Description:** Gets a list of all unique queue names and the count of messages in each queue.

**Curl Examples:**
```sh
curl -X GET http://localhost:8080/queue_names
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
