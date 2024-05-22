### SUQS Simple Universal Queue Service 

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
