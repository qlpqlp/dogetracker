# DogeTracker

DogeTracker is a Go application that tracks Dogecoin addresses, monitoring all payments in and out, and storing transaction history in a PostgreSQL database. It provides a REST API for querying address balances, transaction history, and unspent outputs.

## Features

- Track multiple Dogecoin addresses
- Monitor all incoming and outgoing transactions
- Store transaction history in PostgreSQL
- Track unspent outputs for creating new transactions
- Handle blockchain reorganizations
- REST API with authentication
- Real-time updates

## Requirements

- Go 1.18 or higher
- PostgreSQL database
- Dogecoin node with RPC access

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

### Track a new address

```
POST /api/track
Authorization: Bearer your_api_token
Content-Type: application/json

{
  "address": "D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2"
}
```

### Get address details

```
GET /api/address/D8jfkhj4k3h2jk4h2jk4h2jk4h2jk4h2jk4h2
Authorization: Bearer your_api_token
```

## License

MIT
