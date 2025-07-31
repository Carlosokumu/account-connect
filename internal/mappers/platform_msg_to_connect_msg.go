package mappers

import (
	pb "account-connect/gen"
	"account-connect/internal/messages"
	"fmt"
	"strconv"

	"github.com/adshao/go-binance/v2"
)

// ProtoOADealToAccountConnectDeal converts a list of ProtoOADeal messages from a ProtoOADealListRes
// into a slice of AccountConnectDeal domain objects used by the application.
// It maps relevant fields such as execution price, commission, direction, and symbol.
func ProtoOADealToAccountConnectDeal(r *pb.ProtoOADealListRes) []messages.AccountConnectDeal {
	var deals []messages.AccountConnectDeal

	for _, deal := range r.Deal {
		deal := messages.AccountConnectDeal{
			ExecutionPrice: deal.ExecutionPrice,
			Commission:     deal.Commission,
			Direction:      deal.TradeSide.String(),
			Symbol:         deal.SymbolId,
		}
		deals = append(deals, deal)
	}
	return deals
}

// ProtoOATraderToaccountConnectTrader converts a ProtoOATraderRes message into an AccountConnectTraderInfo
// domain object. It extracts trader account ID, login, deposit asset ID, and broker name.
func ProtoOATraderToaccountConnectTrader(r *pb.ProtoOATraderRes) messages.AccountConnectTraderInfo {
	return messages.AccountConnectTraderInfo{
		CtidTraderAccountId: r.CtidTraderAccountId,
		Login:               r.Trader.TraderLogin,
		DepositAssetId:      r.Trader.DepositAssetId,
		BrokerName:          r.Trader.BrokerName,
	}
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
		accsym := messages.AccountConnectSymbol{
			SymbolName: sym.SymbolName,
			SymbolId:   sym.SymbolId,
		}
		symList = append(symList, accsym)
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
		if sym.Status == "TRADING" {
			accsym := messages.AccountConnectSymbol{
				SymbolName: &sym.Symbol,
				SymbolId:   sym.Symbol,
			}
			accsyms = append(accsyms, accsym)
		}
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
