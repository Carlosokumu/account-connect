package common

import messages "account-connect/gen"

var (

	//Message requests
	AppAuthMsgType           = uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_REQ)
	AccountAuthMsgType       = uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_REQ)
	HeartBeatMsgType         = uint32(messages.ProtoPayloadType_HEARTBEAT_EVENT)
	AccountHistoricalDeals   = uint32(messages.ProtoOAPayloadType_PROTO_OA_DEAL_LIST_REQ)
	RefreshTokenMsgType      = uint32(messages.ProtoOAPayloadType_PROTO_OA_REFRESH_TOKEN_REQ)
	TraderInfoMsgType        = uint32(messages.ProtoOAPayloadType_PROTO_OA_TRADER_REQ)
	TrendBarsMsyType         = uint32(messages.ProtoOAPayloadType_PROTO_OA_GET_TRENDBARS_REQ)
	AccountSymbolListMsgType = uint32(messages.ProtoOAPayloadType_PROTO_OA_SYMBOLS_LIST_REQ)
	AccountSymbolInfo        = uint32(messages.ProtoOAPayloadType_PROTO_OA_SYMBOL_BY_ID_REQ)

	//Proto Message responses
	SymbolListRes        = uint32(messages.ProtoOAPayloadType_PROTO_OA_SYMBOLS_LIST_RES)
	AccountSymbolInfoRes = uint32(messages.ProtoOAPayloadType_PROTO_OA_SYMBOL_BY_ID_RES)
	TrendBarsRes         = uint32(messages.ProtoOAPayloadType_PROTO_OA_GET_TRENDBARS_RES)
	TraderInfoRes        = uint32(messages.ProtoOAPayloadType_PROTO_OA_TRADER_RES)
	TokenRes             = uint32(messages.ProtoOAPayloadType_PROTO_OA_REFRESH_TOKEN_RES)
	DealsRes             = uint32(messages.ProtoOAPayloadType_PROTO_OA_DEAL_LIST_RES)
	ErrorRes             = uint32(messages.ProtoOAPayloadType_PROTO_OA_ERROR_RES)
	AccountAuthRes       = uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_RES)
	ApplicationAthRes    = uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_RES)
	HeartBeatRes         = uint32(messages.ProtoPayloadType_HEARTBEAT_EVENT)
)
