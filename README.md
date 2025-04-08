# DogeTracker

Much wow! DogeTracker is a Go application that tracks Dogecoin addresses, monitoring all payments in and out, and storing transaction history in a PostgreSQL database. It provides a REST API for querying address balances, transaction history, and unspent outputs. Such tracking, very blockchain!

## Features

- Track multiple Dogecoin addresses (Many addresses, much tracking!)
- Monitor all incoming and outgoing transactions (So transaction, very monitor!)
- Store transaction history in PostgreSQL (Such database, much storage!)
- Track unspent outputs for creating new transactions (Many outputs, very unspent!)
- Handle blockchain reorganizations (Such reorganization, very handle!)
- REST API with authentication (Much secure, very API!)
- Real-time updates (So real-time, much update!)

## Requirements

- Go 1.18 or higher (Much Go, very version!)
- PostgreSQL database (Such database, very PostgreSQL!)
- Dogecoin node with RPC access (Many node, much Dogecoin!)

## Installation and Setup

### Installing Go

#### Windows
1. Download the Go installer from [golang.org](https://golang.org/dl/)
2. Run the installer and follow the prompts
3. Verify installation by opening a command prompt and running:
   ```bash
   go version
   ```

#### macOS
```bash
# Using Homebrew
brew install go

# Verify installation
go version
```

#### Linux (Ubuntu/Debian)
```bash
# Add the repository
sudo add-apt-repository ppa:longsleep/golang-backports
sudo apt update

# Install Go
sudo apt install golang-go

# Verify installation
go version
```

### Compiling DogeTracker

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/dogetracker.git
   cd dogetracker
   ```

2. Build the application:
   ```bash
   go build -o dogetracker
   ```

3. Run the application:
   ```bash
   ./dogetracker
   ```

### Running DogeTracker

After compiling, you can run DogeTracker with default settings:

```bash
./dogetracker \
  --db-host=localhost \
  --db-port=5432 \
  --db-user=postgres \
  --db-pass=postgres \
  --db-name=dogetracker \
  --rpc-host=127.0.0.1 \
  --rpc-port=22555 \
  --rpc-user=dogecoin \
  --rpc-pass=dogecoin \
  --api-port=420 \
  --api-token=your_api_token
```

## Quick Start with Docker

Much Docker, very container! Here's how to quickly set up the required PostgreSQL database using Docker:

### Running PostgreSQL in Docker

```bash
# Run PostgreSQL container
docker run --name postgres-dogetracker \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=dogetracker \
  -d -p 5432:5432 postgres:15

# Verify the container is running
docker ps
```

This command:
- Creates a PostgreSQL container named `postgres-dogetracker`
- Sets the default username to `postgres`
- Sets the password to `postgres`
- Creates a database named `dogetracker`
- Maps the container's port 5432 to the host's port 5432
- Uses PostgreSQL version 15

### Running DogeTracker with Docker PostgreSQL

Once your PostgreSQL container is running, you can start DogeTracker with the default database settings:

```bash
./dogetracker \
  --db-host=localhost \
  --db-port=5432 \
  --db-user=postgres \
  --db-pass=postgres \
  --db-name=dogetracker
```

Or if you prefer to use environment variables:

```bash
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=postgres
export DB_PASS=postgres
export DB_NAME=dogetracker
./dogetracker
```

## Configuration

DogeTracker can be configured using command-line flags or environment variables:

```
Usage of dogetracker:
./dogetracker
  -api-port int
        API server port (default 420)
  -api-token string
        API bearer token for authentication
  -db-host string
        PostgreSQL host (default "localhost")
  -db-name string
        PostgreSQL database name (default "dogetracker")
  -db-pass string
        PostgreSQL password (default "postgres")
  -db-port int
        PostgreSQL port (default 5432)
  -db-user string
        PostgreSQL username (default "postgres")
  -rpc-host string
        Dogecoin RPC host (default "127.0.0.1")
  -rpc-pass string
        Dogecoin RPC password (default "dogecoin")
  -rpc-port int
        Dogecoin RPC port (default 22555)
  -rpc-user string
        Dogecoin RPC username (default "dogecoin")
  -start-block string
        Starting block hash or height to begin processing from (default "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n")
  -zmq-host string
        Dogecoin ZMQ host (default "127.0.0.1")
  -zmq-port int
        Dogecoin ZMQ port (default 28332)
```

## API Endpoints

Much API, very endpoints! Here are the available API endpoints with code examples:

### Track a new address

Such tracking, very address! Add a new Dogecoin address to track:

```
POST /api/track
Authorization: Bearer your_api_token
Content-Type: application/json

{
  "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
  "required_confirmations": 3
}
```

#### cURL Example
```bash
curl -X POST \
  http://localhost:420/api/track \
  -H 'Authorization: Bearer your_api_token' \
  -H 'Content-Type: application/json' \
  -d '{
    "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
    "required_confirmations": 3
}'
```

#### Python Example
```python
import requests

url = "http://localhost:420/api/track"
headers = {
    "Authorization": "Bearer your_api_token",
    "Content-Type": "application/json"
}
data = {
    "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
    "required_confirmations": 3
}

response = requests.post(url, headers=headers, json=data)
print(response.json())
```

#### Example Response
```json
{
  "address": {
    "id": 1,
    "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
    "balance": 1000.5,
    "required_confirmations": 3,
    "created_at": "2023-06-15T14:30:00Z",
    "updated_at": "2023-06-15T14:30:00Z"
  },
  "transactions": [
    {
      "id": 1,
      "address_id": 1,
      "tx_id": "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
      "block_hash": "b1c2d3e4f5g6h7i8j9k0l1m2n3o4p5q6r7s8t9u0v1w2x3y4z5a6",
      "block_height": 4500000,
      "amount": 1000.5,
      "is_incoming": true,
      "confirmations": 50,
      "status": "confirmed",
      "created_at": "2023-06-15T14:30:00Z"
    }
  ],
  "unspent_outputs": [
    {
      "id": 1,
      "address_id": 1,
      "tx_id": "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
      "vout": 0,
      "amount": 1000.5,
      "script": "76a914a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a688ac",
      "created_at": "2023-06-15T14:30:00Z"
    }
  ]
}
```

### Get address details

Much details, very address! Get information about a tracked Dogecoin address:

```
GET /api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n
Authorization: Bearer your_api_token
```

#### cURL Example
```bash
curl -X GET \
  http://localhost:420/api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:420/api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
```

#### Example Response
```json
{
  "address": {
    "id": 1,
    "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
    "balance": 1000.5,
    "required_confirmations": 3,
    "created_at": "2023-06-15T14:30:00Z",
    "updated_at": "2023-06-15T14:30:00Z"
  },
  "transactions": [
    {
      "id": 1,
      "address_id": 1,
      "tx_id": "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
      "block_hash": "b1c2d3e4f5g6h7i8j9k0l1m2n3o4p5q6r7s8t9u0v1w2x3y4z5a6",
      "block_height": 4500000,
      "amount": 1000.5,
      "is_incoming": true,
      "confirmations": 50,
      "status": "confirmed",
      "created_at": "2023-06-15T14:30:00Z"
    },
    {
      "id": 2,
      "address_id": 1,
      "tx_id": "b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a7",
      "block_hash": null,
      "block_height": null,
      "amount": -500.0,
      "is_incoming": false,
      "confirmations": 0,
      "status": "pending",
      "created_at": "2023-06-16T10:15:00Z"
    }
  ],
  "unspent_outputs": [
    {
      "id": 1,
      "address_id": 1,
      "tx_id": "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
      "vout": 0,
      "amount": 1000.5,
      "script": "76a914a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a688ac",
      "created_at": "2023-06-15T14:30:00Z"
    }
  ]
}
```

### Get all tracked addresses

Many addresses, very list! Get a list of all tracked Dogecoin addresses:

```
GET /api/addresses
Authorization: Bearer your_api_token
```

#### cURL Example
```bash
curl -X GET \
  http://localhost:420/api/addresses \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:420/api/addresses"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
```

#### Example Response
```json
[
  {
    "id": 1,
    "address": "DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n",
    "balance": 1000.5,
    "required_confirmations": 3,
    "created_at": "2023-06-15T14:30:00Z",
    "updated_at": "2023-06-15T14:30:00Z"
  },
  {
    "id": 2,
    "address": "D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2",
    "balance": 500.0,
    "required_confirmations": 1,
    "created_at": "2023-06-16T09:45:00Z",
    "updated_at": "2023-06-16T09:45:00Z"
  }
]
```

### Get unspent outputs

Such outputs, very unspent! Get all unspent outputs for a tracked address:

```
GET /api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n/unspent
Authorization: Bearer your_api_token
```

#### cURL Example
```bash
curl -X GET \
  http://localhost:420/api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n/unspent \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:420/api/address/DTqAFgNNUgiPEfGmc4HZUkqJ4sz5vADd1n/unspent"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
```

#### Example Response
```json
[
  {
    "id": 1,
    "address_id": 1,
    "tx_id": "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6",
    "vout": 0,
    "amount": 1000.5,
    "script": "76a914a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a688ac",
    "created_at": "2023-06-15T14:30:00Z"
  },
  {
    "id": 2,
    "address_id": 1,
    "tx_id": "c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a7b8",
    "vout": 1,
    "amount": 500.0,
    "script": "76a914a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a688ac",
    "created_at": "2023-06-16T11:20:00Z"
  }
]
```

## License

MIT - Much license, very open source!

## Acknowledgments

DogeTracker was inspired by [DogeWalker](https://github.com/dogeorg/dogewalker), a Go library for high-performance chain tracking against Dogecoin Core created by [@raffecat](https://github.com/raffecat) and [@tjstebbing](https://github.com/tjstebbing). Much inspiration, very thanks!

## Future Plans

DogeTracker will soon be ported as a PUP to [DogeBox](https://dogebox.dogecoin.org), making it even easier to deploy and manage. Stay tuned for updates!

---

*Much wow! Such tracking! Very Dogecoin!*
