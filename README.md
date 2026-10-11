# account-connect


account-connect is a Go service that normalizes and streams financial market data from different providers through a **single WebSocket connection**. Instead of integrating each broker's SDK, protocol and quirks separately, your client speaks one message format and account-connect handles the rest.

| Provider | Markets | Status |
|----------|---------|--------|
| **Binance** | Crypto (spot, margin, futures, delivery) | Supported |
| **cTrader** | Forex and CFDs | Supported |
| **Alpaca** | US stocks, plus crypto | Supported |

## How it works

```
Client ──WebSocket──▶ account-connect ──▶ Binance (WebSocket)
                                      ├─▶ cTrader (protobuf)
                                      └─▶ Alpaca  (SDK)
```

- **One connection, many providers.** A single client can connect to several providers at once; their streams are merged and delivered over the same socket.
- **Unified messages.** Every request and response uses the same envelope, with a `provider` field saying where it goes or came from, regardless of the data provider's native format.
- **Account IDs.** When you connect an account, the service returns an `account_id`. Use it in all later messages to say which account you mean. A Binance client with spot and futures connected gets a separate `account_id` for each.

## Configuration

1. Copy the example configuration file:

   ```bash
   cp config.example.yml config.yml
   ```

2. Edit `config.yml`. The only mandatory setting is the server port:

   ```yaml
   account-connect-server:
     port: 8080   # the port the service listens on
   ```

3. Everything else is optional. Fill in credentials only for the providers you want to use.

## Running the service

```bash
make run
```

This will:

- Start a WebSocket server on your configured port
- Initialize the connection handler
- Prepare the service to receive account-connect messages

## Messages

All messages are JSON over the WebSocket and share one envelope:

| Field | Description |
|-------|-------------|
| `messagetype` | What the message is, for example `ohlc.history` |
| `provider` | Which provider the message goes to or came from (`binance`, `ctrader`, `alpaca`) |
| `tradeshare_client_id` | Identifies your client |
| `request_id` | Your own ID for a request. It is echoed back on the response so you can match them up |
| `status` | Present on responses: `success`, or an error |
| `payload` | The message-specific data |

### Message types

| Category | Message types | What it does |
|----------|---------------|--------------|
| **Session** | `session.connect`, `session.disconnect`, `error` | Open and close a connection to a provider. `error` is returned when a request fails. |
| **Account** | `account.authorize`, `account.info`, `account.orders`, `account.trades` | Authorize an account, then fetch its details and balances, orders and trades. |
| **Symbols** | `symbols.list` | List the instruments a provider offers, enriched with base/quote asset names (for example "Bitcoin" for BTC). |
| **Historical data** | `ohlc.history`, `ticks.history`, `depth.snapshot` | Request-and-response data: past candlesticks, past ticks, or an order book snapshot. |
| **Live streams** | `ohlc.*`, `ticks.*`, `depth.*`, `bbo.*` | Subscribe to live data and receive updates as they arrive (see below). |

### Subscriptions

Each live stream follows the same pattern: send `<stream>.subscribe` to start, receive `<stream>.update` messages as data arrives, and send `<stream>.unsubscribe` to stop.

| Stream | Subscribe | Update | Unsubscribe |
|--------|-----------|--------|-------------|
| Candlesticks (OHLC) | `ohlc.subscribe` | `ohlc.update` | `ohlc.unsubscribe` |
| Ticks (trades) | `ticks.subscribe` | `ticks.update` | `ticks.unsubscribe` |
| Order book depth | `depth.subscribe` | `depth.update` | `depth.unsubscribe` |
| Best bid/offer | `bbo.subscribe` | `bbo.update` | `bbo.unsubscribe` |

### Provider support by category

| Category | Binance | cTrader | Alpaca |
|----------|:-------:|:-------:|:------:|
| Connection | ✅ | ✅ | ✅ |
| Account info | ✅ | ✅ | ✅ |
| Symbols | ✅ | ✅ | ✅ |
| Historical candles | ✅ | ✅ | ✅ |
| Live candle stream | ✅ | ✅ | ✅ |

> Messages that are not implemented yet for a provider respond with a "not yet supported" error.

### Example: historical candles

Request one day of 1-minute candles for a symbol:

```json
{
  "messagetype": "ohlc.history",
  "tradeshare_client_id": "foo",
  "provider": "alpaca",
  "request_id": "12345666",
  "payload": {
    "account_id": 47758608,
    "symbol_name": "FLOC",
    "period": "1m",
    "from_timestamp": 1789084800000,
    "to_timestamp": 1789171200000
  }
}
```

Response (truncated):

```json
{
  "messagetype": "ohlc.history",
  "status": "success",
  "provider": "alpaca",
  "tradeshare_client_id": "foo",
  "request_id": "12345666",
  "payload": {
    "symbol": "FLOC",
    "period": "1m",
    "trendbars": [
      {
        "open": 20.45,
        "high": 20.45,
        "low": 20.12,
        "close": 20.33,
        "volume": 8383,
        "utcTimeStampInMinutes": 29818890
      },
      {
        "open": 20.69,
        "high": 20.69,
        "low": 20.69,
        "close": 20.69,
        "volume": 573,
        "utcTimeStampInMinutes": 29818891
      },
      {
        "open": 20.73,
        "high": 20.73,
        "low": 20.63,
        "close": 20.63,
        "volume": 1625,
        "utcTimeStampInMinutes": 29818892
      }
      // ...more bars
    ]
  }
}
```

## Behavior notes

- Connections are not revalidated in the background. If a connection has gone invalid, the next request using it returns an error.

