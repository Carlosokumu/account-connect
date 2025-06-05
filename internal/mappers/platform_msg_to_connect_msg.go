package mappers

import (
	pb "account-connect/gen"
	"fmt"
)

func ProtoOADealToAccountConnectDeal(r *pb.ProtoOADealListRes) []AccountConnectDeal {
	var deals []AccountConnectDeal

	for _, deal := range r.Deal {
		deal := AccountConnectDeal{
			ExecutionPrice: deal.ExecutionPrice,
			Commission:     deal.Commission,
		}
		deals = append(deals, deal)
	}
	return deals
}

func ProtoOATraderToaccountConnectTrader(r *pb.ProtoOATraderRes) AccountConnectTraderInfo {
	return AccountConnectTraderInfo{
		CtidTraderAccountId: r.CtidTraderAccountId,
		Login:               r.Trader.TraderLogin,
		DepositAssetId:      r.Trader.DepositAssetId,
		BrokerName:          r.Trader.BrokerName,
	}
}

func ProotoOAToTrendBars(r *pb.ProtoOAGetTrendbarsRes) []AccountConnectTrendBar {
	var trendBars []AccountConnectTrendBar
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

		tBar := AccountConnectTrendBar{
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

func ProtoOAErrorResToError(r *pb.ProtoOAErrorRes) *AccountConnectError {
	return &AccountConnectError{
		Description: *r.Description,
	}
}

func ProtoSymbolListResponseToAccountConnectSymbol(r *pb.ProtoOASymbolsListRes) []AccountConnectSymbol {
	var symList []AccountConnectSymbol

	for _, sym := range r.Symbol {
		accsym := AccountConnectSymbol{
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
