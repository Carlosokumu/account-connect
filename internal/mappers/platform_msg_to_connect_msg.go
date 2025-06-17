package mappers

import (
	pb "account-connect/gen"
	"account-connect/internal/messages"
	"fmt"

	"github.com/adshao/go-binance/v2"
)

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

func ProtoOATraderToaccountConnectTrader(r *pb.ProtoOATraderRes) messages.AccountConnectTraderInfo {
	return messages.AccountConnectTraderInfo{
		CtidTraderAccountId: r.CtidTraderAccountId,
		Login:               r.Trader.TraderLogin,
		DepositAssetId:      r.Trader.DepositAssetId,
		BrokerName:          r.Trader.BrokerName,
	}
}

func ProotoOAToTrendBars(r *pb.ProtoOAGetTrendbarsRes) []messages.AccountConnectTrendBar {
	var trendBars []messages.AccountConnectTrendBar
	for _, trendBar := range r.Trendbar {
		low := int64(0)
		if trendBar.Low != nil {
			low = *trendBar.Low
		}

		deltaHigh := uint64(0)
		if trendBar.DeltaHigh != nil {
			deltaHigh = *trendBar.DeltaHigh
		}

		deltaOpen := uint64(0)
		if trendBar.DeltaOpen != nil {
			deltaOpen = *trendBar.DeltaOpen
		}

		deltaClose := uint64(0)
		if trendBar.DeltaClose != nil {
			deltaClose = *trendBar.DeltaClose
		}

		tBar := messages.AccountConnectTrendBar{
			Low:                   low,
			High:                  low + int64(deltaHigh),
			Open:                  low + int64(deltaOpen),
			Close:                 low + int64(deltaClose),
			UtcTimestampInMinutes: int64(*trendBar.UtcTimestampInMinutes),
		}
		trendBars = append(trendBars, tBar)
	}

	return trendBars
}

func ProtoOAErrorResToError(r *pb.ProtoOAErrorRes) *messages.AccountConnectError {
	return &messages.AccountConnectError{
		Description: *r.Description,
	}
}

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
func BinanceSymbolToAccountConnectSymbol(binancesyms []binance.Symbol) []messages.AccountConnectSymbol {
	var accsyms []messages.AccountConnectSymbol

	for _, sym := range binancesyms {
		//The symbol from binance will be used as an id and symbol name
		accsym := messages.AccountConnectSymbol{
			SymbolName: &sym.Symbol,
			SymbolId:   sym.Symbol,
		}
		accsyms = append(accsyms, accsym)
	}
	return accsyms
}
