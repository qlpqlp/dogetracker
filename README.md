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

## Configuration

DogeTracker can be configured using command-line flags or environment variables:

```
Usage of dogetracker:
  -api-port int
        API server port (default 8080)
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
  "address": "D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2",
  "required_confirmations": 3
}
```

#### cURL Example
```bash
curl -X POST \
  http://localhost:8080/api/track \
  -H 'Authorization: Bearer your_api_token' \
  -H 'Content-Type: application/json' \
  -d '{
    "address": "D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2",
    "required_confirmations": 3
}'
```

#### Python Example
```python
import requests

url = "http://localhost:8080/api/track"
headers = {
    "Authorization": "Bearer your_api_token",
    "Content-Type": "application/json"
}
data = {
    "address": "D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2",
    "required_confirmations": 3
}

response = requests.post(url, headers=headers, json=data)
print(response.json())
```

### Get address details

Much details, very address! Get information about a tracked Dogecoin address:

```
GET /api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2
Authorization: Bearer your_api_token
```

#### cURL Example
```bash
curl -X GET \
  http://localhost:8080/api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2 \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:8080/api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
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
  http://localhost:8080/api/addresses \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:8080/api/addresses"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
```

### Get unspent outputs

Such outputs, very unspent! Get all unspent outputs for a tracked address:

```
GET /api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2/unspent
Authorization: Bearer your_api_token
```

#### cURL Example
```bash
curl -X GET \
  http://localhost:8080/api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2/unspent \
  -H 'Authorization: Bearer your_api_token'
```

#### Python Example
```python
import requests

url = "http://localhost:8080/api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2/unspent"
headers = {
    "Authorization": "Bearer your_api_token"
}

response = requests.get(url, headers=headers)
print(response.json())
```

## License

MIT - Much license, very open source!
