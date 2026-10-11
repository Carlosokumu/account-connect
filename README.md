# account-connect


account-connect is a Go service that normalizes and streams financial market data from different providers through a **single WebSocket connection**. Instead of integrating each broker's SDK, protocol and quirks separately, your client speaks one message format and account-connect handles the rest.

| Provider | Markets | Status |
|----------|---------|--------|
| **Binance** | Crypto (spot, margin, futures, delivery) | Supported |
| **cTrader** | Forex and CFDs | Supported  |
| **Alpaca** | US stocks, plus crypto | Supported |

Messages that have not yet been  implemented for the provider, the service will respond with a "not yet supported"  error.

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

All messages are JSON over the WebSocket. Each one carries a message `type` and, where relevant, a `provider` and an `account_id`.

### Message categories

| Category | What it does | Examples |
|----------|--------------|----------|
| **Connection** | Connect an account at a provider and receive its `account_id`. |
| **Account** | Fetch account details and balances for a connected account. | `account_info` |
| **Symbols** | List the instruments available on a provider, enriched with base/quote asset names (for example "Bitcoin" for BTC). | symbol list |
| **Historical data** | Request past candlesticks (OHLC) for a symbol. Symbols are given by name on every provider. | trend bars / candles |
| **Streaming** | Subscribe to live data for a symbol and receive updates as they arrive. | `ohlc_stream` |


### Provider support by category

| Category | Binance | cTrader | Alpaca |
|----------|:-------:|:-------:|:------:|
| Connection | ✅ | ✅ | ✅ |
| Account info | ✅ | ✅ | ✅  |
| Symbols | ✅ | ✅ | ✅  |
| Historical candles | ✅ | ✅ | ✅  |
| Live candle stream | ✅ | ✅ | ✅  |

> Methods a provider does not support yet respond with a "not yet supported" error.

### Example

```json
{
  "type": "ohlc_stream",
  "provider": "binance",
  "account_id": 1234567890123456789,
  "symbol": "BTCUSDT"
}
```

