package mappers

import (
	pb "account-connect/gen"
	"account-connect/internal/messages"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/delivery"
	"github.com/adshao/go-binance/v2/futures"
	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
)

// ProtoOADealToAccountConnectDeal converts a list of ProtoOADeal messages from a ProtoOADealListRes
// into a slice of AccountConnectDeal domain objects used by the application.
// It maps relevant fields such as execution price, commission, direction, and symbol.
func ProtoOADealToAccountConnectDeal(r *pb.ProtoOADealListRes) []messages.AccountConnectDeal {
	var deals []messages.AccountConnectDeal

	for _, deal := range r.Deal {
		if deal.ClosePositionDetail != nil {
			vdeal := messages.AccountConnectDeal{
				ExecutionPrice: deal.ExecutionPrice,
				Commission:     deal.Commission,
				EntryTime:      deal.ExecutionTimestamp,
				Lots:           deal.Volume,
				Symbol:         deal.SymbolId,
				DealId:         deal.DealId,
				Profit:         deal.ClosePositionDetail.GrossProfit,
				Balance:        deal.ClosePositionDetail.Balance,
				ClosingPrice:   deal.ExecutionPrice,
				EntryPrice:     deal.ClosePositionDetail.EntryPrice,
			}

			if deal.GetTradeSide() == pb.ProtoOATradeSide_BUY {
				vdeal.Direction = "SELL"
			} else if deal.GetTradeSide() == pb.ProtoOATradeSide_SELL {
				vdeal.Direction = "BUY"
			}

			deals = append(deals, vdeal)
		}
	}
	return deals
}

func ProtoOATraderToaccountConnectTrader(r *pb.ProtoOATraderRes) messages.AccountConnectCtraderAccountInfo {
	t := r.Trader
	if t == nil {
		return messages.AccountConnectCtraderAccountInfo{}
	}

	var moneyDigits uint32
	if t.MoneyDigits != nil {
		moneyDigits = *t.MoneyDigits
	}
	scale := math.Pow(10, float64(moneyDigits))

	info := messages.AccountConnectCtraderAccountInfo{
		MoneyDigits: moneyDigits,
	}

	if t.CtidTraderAccountId != nil {
		info.CtidTraderAccountId = *t.CtidTraderAccountId
	}
	if t.Balance != nil {
		info.Balance = float64(*t.Balance) / scale
	}
	if t.ManagerBonus != nil {
		info.ManagerBonus = float64(*t.ManagerBonus) / scale
	}
	if t.IbBonus != nil {
		info.IbBonus = float64(*t.IbBonus) / scale
	}
	if t.NonWithdrawableBonus != nil {
		info.NonWithdrawableBonus = float64(*t.NonWithdrawableBonus) / scale
	}
	if t.DepositAssetId != nil {
		info.DepositAssetId = *t.DepositAssetId
	}
	if t.LeverageInCents != nil {
		info.LeverageInCents = *t.LeverageInCents
	}
	if t.MaxLeverage != nil {
		info.MaxLeverage = *t.MaxLeverage
	}
	if t.SwapFree != nil {
		info.SwapFree = *t.SwapFree
	}
	if t.IsLimitedRisk != nil {
		info.IsLimitedRisk = *t.IsLimitedRisk
	}
	if t.BrokerName != nil {
		info.BrokerName = *t.BrokerName
	}
	if t.TraderLogin != nil {
		info.TraderLogin = *t.TraderLogin
	}
	if t.RegistrationTimestamp != nil {
		info.RegistrationTimestamp = *t.RegistrationTimestamp
	}
	if t.AccountType != nil {
		info.AccountType = t.AccountType.String() // enum's generated String() method
	}
	if t.AccessRights != nil {
		info.AccessRights = t.AccessRights.String()
	}

	return info
}

// ProotoOAToTrendBars converts a ProtoOAGetTrendbarsRes message into a slice of AccountConnectTrendBar objects.
// It computes Open, High, Close prices by applying respective deltas to the Low price, and also extracts volume and timestamp.
func ProotoOAToTrendBars(r *pb.ProtoOAGetTrendbarsRes) []messages.AccountConnectTrendBar {
	var trendBars []messages.AccountConnectTrendBar
	for _, trendBar := range r.Trendbar {
		low := float64(0)
		if trendBar.Low != nil {
			low = float64(*trendBar.Low)
		}

		deltaHigh := float64(0)
		if trendBar.DeltaHigh != nil {
			deltaHigh = float64(*trendBar.DeltaHigh)
		}

		deltaOpen := float64(0)
		if trendBar.DeltaOpen != nil {
			deltaOpen = float64(*trendBar.DeltaOpen)
		}

		deltaClose := float64(0)
		if trendBar.DeltaClose != nil {
			deltaClose = float64(*trendBar.DeltaClose)
		}

		volume := int64(0)
		if trendBar.Volume != nil {
			volume = *trendBar.Volume
		}

		tBar := messages.AccountConnectTrendBar{
			Low:                   low,
			High:                  low + float64(deltaHigh),
			Open:                  low + float64(deltaOpen),
			Close:                 low + float64(deltaClose),
			UtcTimestampInMinutes: *trendBar.UtcTimestampInMinutes,
			Volume:                volume,
		}
		trendBars = append(trendBars, tBar)
	}

	return trendBars
}

// ProtoOAErrorResToError converts a ProtoOAErrorRes message into an AccountConnectError object.
// It extracts the error description from the response.
func ProtoOAErrorResToError(r *pb.ProtoOAErrorRes) *messages.AccountConnectError {
	return &messages.AccountConnectError{
		Description: *r.Description,
	}
}

// ProtoSymbolListResponseToAccountConnectSymbol converts a ProtoOASymbolsListRes message
// into a slice of AccountConnectSymbol objects by mapping each symbol's name and ID.
func ProtoSymbolListResponseToAccountConnectSymbol(r *pb.ProtoOASymbolsListRes) []messages.AccountConnectSymbol {
	var symList []messages.AccountConnectSymbol

	for _, sym := range r.Symbol {
		sym := messages.AccountConnectSymbol{
			SymbolName:         *sym.Description,
			SymbolAbbreviation: *sym.SymbolName,
			Ctrader: &messages.CtraderSymbolInfo{
				SymbolId:    *sym.SymbolId,
				SymbolName:  *sym.SymbolName,
				BaseAssetId: *sym.BaseAssetId,

				QuoteAssetId: *sym.QuoteAssetId,
			}}
		symList = append(symList, sym)
	}
	return symList
}

//	PeriodStrToBarPeriod maps a string-based time period (e.g., "M1", "H1", "D1")
//
// to its corresponding ProtoOATrendbarPeriod enum value used in gRPC requests.
// Returns an error if the input string is not a recognized period.
func PeriodStrToBarPeriod(periodStr string) (pb.ProtoOATrendbarPeriod, error) {
	switch periodStr {
	case "M1":
		return pb.ProtoOATrendbarPeriod_M1, nil
	case "M2":
		return pb.ProtoOATrendbarPeriod_M2, nil
	case "M3":
		return pb.ProtoOATrendbarPeriod_M3, nil
	case "M4":
		return pb.ProtoOATrendbarPeriod_M4, nil
	case "M5":
		return pb.ProtoOATrendbarPeriod_M5, nil
	case "M10":
		return pb.ProtoOATrendbarPeriod_M10, nil
	case "M15":
		return pb.ProtoOATrendbarPeriod_M15, nil
	case "M30":
		return pb.ProtoOATrendbarPeriod_M30, nil
	case "H1":
		return pb.ProtoOATrendbarPeriod_H1, nil
	case "H4":
		return pb.ProtoOATrendbarPeriod_H4, nil
	case "H12":
		return pb.ProtoOATrendbarPeriod_H12, nil
	case "D1":
		return pb.ProtoOATrendbarPeriod_D1, nil
	case "W1":
		return pb.ProtoOATrendbarPeriod_W1, nil
	case "MN1":
		return pb.ProtoOATrendbarPeriod_MN1, nil
	default:
		return 0, fmt.Errorf("invalid period: %s", periodStr)
	}
}

// BinanceSymbolToAccountConnectSymbol filters Binance symbols with status "TRADING"
// and converts them into AccountConnectSymbol objects. The Binance symbol string is
// used as both the ID and the name.
func BinanceSymbolToAccountConnectSymbol(binancesyms []binance.Symbol) []messages.AccountConnectSymbol {
	var accsyms []messages.AccountConnectSymbol

	for _, sym := range binancesyms {
		if sym.Status != "TRADING" {
			continue
		}
		accsyms = append(accsyms, messages.AccountConnectSymbol{
			SymbolAbbreviation: sym.Symbol,
			Binance: &messages.BinanceSymbolInfo{
				BaseAsset:              sym.BaseAsset,
				QuoteAsset:             sym.QuoteAsset,
				BaseAssetPrecision:     sym.BaseAssetPrecision,
				QuoteAssetPrecision:    sym.QuoteAssetPrecision,
				IsSpotTradingAllowed:   sym.IsSpotTradingAllowed,
				IsMarginTradingAllowed: sym.IsMarginTradingAllowed,
				Permissions:            sym.Permissions,
			},
		})
	}
	return accsyms
}

// BinanceKlineDataToAccountConnectTrendBar converts Binance OHLC (Kline) data into a slice of
// AccountConnectTrendBar objects. It parses string-based price and volume fields into floats,
// calculates the open time in minutes, and returns a structured result.
// Returns an error if any numeric field fails to parse.
func BinanceKlineDataToAccountConnectTrendBar(ohlc []*binance.Kline) ([]messages.AccountConnectTrendBar, error) {
	var acctrendbars []messages.AccountConnectTrendBar

	for _, kline := range ohlc {
		high, err := strconv.ParseFloat(kline.High, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse High: %v", err)
		}

		low, err := strconv.ParseFloat(kline.Low, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Low: %v", err)
		}

		close, err := strconv.ParseFloat(kline.Close, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Close: %v", err)
		}

		open, err := strconv.ParseFloat(kline.Open, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Open: %v", err)
		}

		volume, err := strconv.ParseFloat(kline.Volume, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Volume: %v", err)
		}
		openTimeMinutes := kline.OpenTime / (1000 * 60)

		acctrendbars = append(acctrendbars, messages.AccountConnectTrendBar{
			High:                  high,
			Low:                   low,
			Close:                 close,
			UtcTimestampInMinutes: uint32(openTimeMinutes),
			Open:                  open,
			Volume:                int64(volume),
		})
	}

	return acctrendbars, nil
}

// PeriodStrToDuration maps a string-based time period (e.g., "M1", "H1", "D1")
// to its corresponding time.Duration, used for client-side candle bucket boundaries
// and close-time calculations when handling cTrader live trend bar pushes.
// Returns an error if the input string is not a recognized period.
func PeriodStrToDuration(periodStr string) (time.Duration, error) {
	switch periodStr {
	case "M1":
		return time.Minute, nil
	case "M2":
		return 2 * time.Minute, nil
	case "M3":
		return 3 * time.Minute, nil
	case "M4":
		return 4 * time.Minute, nil
	case "M5":
		return 5 * time.Minute, nil
	case "M10":
		return 10 * time.Minute, nil
	case "M15":
		return 15 * time.Minute, nil
	case "M30":
		return 30 * time.Minute, nil
	case "H1":
		return time.Hour, nil
	case "H4":
		return 4 * time.Hour, nil
	case "H12":
		return 12 * time.Hour, nil
	case "D1":
		return 24 * time.Hour, nil
	case "W1":
		return 7 * 24 * time.Hour, nil
	case "MN1":
		return 0, fmt.Errorf("period %s has no fixed duration (calendar month), not supported for client-side bucketing", periodStr)
	default:
		return 0, fmt.Errorf("invalid period: %s", periodStr)
	}
}

// FuturesKlinesToBinanceKlines converts futures.Kline slice to binance.Kline slice
// so downstream mappers (BinanceKlineDataToAccountConnectTrendBar) can handle
// all account types without branching.
func FuturesKlinesToBinanceKlines(fk []*futures.Kline) []*binance.Kline {
	result := make([]*binance.Kline, len(fk))
	for i, k := range fk {
		result[i] = &binance.Kline{
			OpenTime:                 k.OpenTime,
			Open:                     k.Open,
			High:                     k.High,
			Low:                      k.Low,
			Close:                    k.Close,
			Volume:                   k.Volume,
			CloseTime:                k.CloseTime,
			QuoteAssetVolume:         k.QuoteAssetVolume,
			TradeNum:                 k.TradeNum,
			TakerBuyBaseAssetVolume:  k.TakerBuyBaseAssetVolume,
			TakerBuyQuoteAssetVolume: k.TakerBuyQuoteAssetVolume,
		}
	}
	return result
}

// DeliveryKlinesToBinanceKlines converts delivery.Kline slice to binance.Kline slice
// for the same reason as FuturesKlinesToBinanceKlines.
func DeliveryKlinesToBinanceKlines(dk []*delivery.Kline) []*binance.Kline {
	result := make([]*binance.Kline, len(dk))
	for i, k := range dk {
		result[i] = &binance.Kline{
			OpenTime:                 k.OpenTime,
			Open:                     k.Open,
			High:                     k.High,
			Low:                      k.Low,
			Close:                    k.Close,
			Volume:                   k.Volume,
			CloseTime:                k.CloseTime,
			QuoteAssetVolume:         k.QuoteAssetVolume,
			TradeNum:                 k.TradeNum,
			TakerBuyBaseAssetVolume:  k.TakerBuyBaseAssetVolume,
			TakerBuyQuoteAssetVolume: k.TakerBuyQuoteAssetVolume,
		}
	}
	return result
}

// FuturesSymbolToAccountConnectSymbol filters futures symbols with ContractStatus "TRADING"
// and converts them into AccountConnectSymbol objects.
func FuturesSymbolToAccountConnectSymbol(syms []futures.Symbol) []messages.AccountConnectSymbol {
	var accsyms []messages.AccountConnectSymbol

	for _, sym := range syms {
		if sym.Status != "TRADING" {
			continue
		}
		accsyms = append(accsyms, messages.AccountConnectSymbol{
			SymbolAbbreviation: sym.Symbol,
			Binance: &messages.BinanceSymbolInfo{
				BaseAsset:          sym.BaseAsset,
				QuoteAsset:         sym.QuoteAsset,
				BaseAssetPrecision: sym.BaseAssetPrecision,
			},
		})
	}

	return accsyms
}

// DeliverySymbolToAccountConnectSymbol filters delivery symbols with ContractStatus "TRADING"
// and converts them into AccountConnectSymbol objects.
func DeliverySymbolToAccountConnectSymbol(syms []delivery.Symbol) []messages.AccountConnectSymbol {
	var accsyms []messages.AccountConnectSymbol
	for _, sym := range syms {
		if sym.ContractStatus != "TRADING" {
			continue
		}
		accsyms = append(accsyms, messages.AccountConnectSymbol{
			SymbolAbbreviation: sym.Symbol,
			Binance: &messages.BinanceSymbolInfo{
				BaseAsset:          sym.BaseAsset,
				QuoteAsset:         sym.QuoteAsset,
				BaseAssetPrecision: sym.BaseAssetPrecision,
			},
		})
	}

	return accsyms
}

// BinanceTradeToAccountConnectDeal converts a single binance.Trade into a
// BinanceAccountConnectDeal, parsing string prices/quantities into float64.
func BinanceTradeToAccountConnectDeal(t *binance.TradeV3, symbol string) messages.BinanceAccountConnectDeal {
	price, err := strconv.ParseFloat(t.Price, 64)
	if err != nil {
		log.Printf("Failed to parse price for trade %d: %v", t.ID, err)
	}
	qty, err := strconv.ParseFloat(t.Quantity, 64)
	if err != nil {
		log.Printf("Failed to parse quantity for trade %d: %v", t.ID, err)
	}
	commission, err := strconv.ParseFloat(t.Commission, 64)
	if err != nil {
		log.Printf("Failed to parse commission for trade %d: %v", t.ID, err)
	}
	return messages.BinanceAccountConnectDeal{
		Symbol:          symbol,
		TradeId:         t.ID,
		Price:           price,
		Quantity:        qty,
		Commission:      commission,
		CommissionAsset: t.CommissionAsset,
		Time:            t.Time,
		IsBuyer:         t.IsBuyer,
		IsMaker:         t.IsMaker,
	}
}

func BinanceFuturesTradeToAccountConnectDeal(t *futures.AccountTrade) messages.BinanceAccountConnectDeal {
	price, _ := strconv.ParseFloat(t.Price, 64)
	qty, _ := strconv.ParseFloat(t.Quantity, 64)
	commission, _ := strconv.ParseFloat(t.Commission, 64)
	pnl, _ := strconv.ParseFloat(t.RealizedPnl, 64)
	return messages.BinanceAccountConnectDeal{
		Symbol:          t.Symbol,
		TradeId:         t.ID,
		OrderId:         t.OrderID,
		Price:           price,
		Quantity:        qty,
		Commission:      commission,
		CommissionAsset: t.CommissionAsset,
		Time:            t.Time,
		IsBuyer:         t.Buyer,
		IsMaker:         t.Maker,
		RealizedPnl:     pnl,
		Side:            string(t.Side),
	}
}

func ProtoOAReconcileToAccountConnectOrder(r *pb.ProtoOAReconcileRes) []messages.AccountConnectOrder {
	var accOrders []messages.AccountConnectOrder

	for _, order := range r.Order {
		accOrder := messages.AccountConnectOrder{
			ExecutionPrice: order.ExecutionPrice,
			OrderId:        order.OrderId,
			OrderType:      order.OrderType.String(),
		}
		accOrders = append(accOrders, accOrder)
	}

	return accOrders
}

// QuoteTypeStrToProto maps a "bid"/"ask" string (case-insensitive) to cTrader's
// ProtoOAQuoteType enum, used when requesting historical tick data for one side
// of the book.
func QuoteTypeStrToProto(quoteType string) (pb.ProtoOAQuoteType, error) {
	switch strings.ToLower(quoteType) {
	case "bid":
		return pb.ProtoOAQuoteType_BID, nil
	case "ask":
		return pb.ProtoOAQuoteType_ASK, nil
	default:
		return 0, fmt.Errorf("unknown quote_type: %q (expected \"bid\" or \"ask\")", quoteType)
	}
}

func BinanceAggTradesToTicks(trades []*binance.AggTrade) []messages.AccountConnectTick {
	ticks := make([]messages.AccountConnectTick, 0, len(trades))
	for _, t := range trades {
		price := parseFloat(t.Price)
		qty := parseFloat(t.Quantity)
		ticks = append(ticks, messages.AccountConnectTick{
			Timestamp:        t.Timestamp,
			Price:            price,
			Quantity:         qty,
			QuoteQty:         price * qty,
			IsBuyerMaker:     t.IsBuyerMaker,
			IsBestPriceMatch: t.IsBestPriceMatch,
		})
	}
	return ticks
}

func BinanceFuturesAggTradesToTicks(trades []*futures.AggTrade) []messages.AccountConnectTick {
	ticks := make([]messages.AccountConnectTick, 0, len(trades))
	for _, t := range trades {
		price := parseFloat(t.Price)
		qty := parseFloat(t.Quantity)
		ticks = append(ticks, messages.AccountConnectTick{
			Timestamp:    t.Timestamp,
			Price:        price,
			Quantity:     qty,
			QuoteQty:     price * qty,
			IsBuyerMaker: t.IsBuyerMaker,
		})
	}
	return ticks
}

// binanceDepthToAccountConnectDepth converts go-binance's []Bid/[]Ask (price/quantity
// as strings) into the unified level shape.
func BinanceDepthToAccountConnectDepth(bids, asks []binance.Bid, lastUpdateID int64) messages.AccountConnectDepthRes {
	res := messages.AccountConnectDepthRes{
		Bids:         make([]messages.AccountConnectDepthLevel, 0, len(bids)),
		Asks:         make([]messages.AccountConnectDepthLevel, 0, len(asks)),
		LastUpdateId: lastUpdateID,
	}
	for _, bid := range bids {
		res.Bids = append(res.Bids, messages.AccountConnectDepthLevel{
			Price:    parseFloat(bid.Price),
			Quantity: parseFloat(bid.Quantity),
		})
	}
	for _, ask := range asks {
		res.Asks = append(res.Asks, messages.AccountConnectDepthLevel{
			Price:    parseFloat(ask.Price),
			Quantity: parseFloat(ask.Quantity),
		})
	}
	return res
}

func parseFloat(s string) float64 {
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("Failed to parse float from string %q: %v", s, err)
		return 0
	}
	return val
}

func PeriodStrToAlpacaTimeFrame(period string) (marketdata.TimeFrame, error) {
	switch period {
	case "1m":
		return marketdata.NewTimeFrame(1, marketdata.Min), nil
	case "5m":
		return marketdata.NewTimeFrame(5, marketdata.Min), nil
	case "15m":
		return marketdata.NewTimeFrame(15, marketdata.Min), nil
	case "30m":
		return marketdata.NewTimeFrame(30, marketdata.Min), nil
	case "1h":
		return marketdata.NewTimeFrame(1, marketdata.Hour), nil
	case "4h":
		return marketdata.NewTimeFrame(4, marketdata.Hour), nil
	case "1d":
		return marketdata.NewTimeFrame(1, marketdata.Day), nil
	default:
		return marketdata.TimeFrame{}, fmt.Errorf("unsupported period: %q", period)
	}
}
