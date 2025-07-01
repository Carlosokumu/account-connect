# account-connect

account-connect is  a tool  that normalizes and streams market data from heterogeneous sources e.g. Binance for crypto, cTrader for forex,Alpaca for stock data through a single connection

## Configuration setup

1. Copy the example configuration file:
   ```bash
   cp config.example.yml config.yml

2. Edit the config.yml file with your settings.The only mandatory setting is the account-connect-server.This defines the port where your service will run.
   
    ```
     account-connect-server:
     port: [your-port-number]
    ```
3. All other settings are optional and can be set depending on which data source you want to be connected to.



## Running the service
   To run the service.
   
   ```bash
   make run
  ```

  This will:
  
  - Start a webSocket server listening on your configured port
  - Initialize the connection handler
  - Prepare the service to receive **account-connect messages**

