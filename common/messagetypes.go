package common

import messages "account-connect/gen"

var (
	AppAuthMsgType         = uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_REQ)
	AccountAuthMsgType     = uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_REQ)
	HeartBeatMsgType       = uint32(messages.ProtoPayloadType_HEARTBEAT_EVENT)
	AccountHistoricalDeals = uint32(messages.ProtoOAPayloadType_PROTO_OA_DEAL_LIST_REQ)
	RefreshTokenMsgType    = uint32(messages.ProtoOAPayloadType_PROTO_OA_REFRESH_TOKEN_REQ)
	TraderInfoMsgType      = uint32(messages.ProtoOAPayloadType_PROTO_OA_TRADER_REQ)
	TrendBarsMsyType       = uint32(messages.ProtoOAPayloadType_PROTO_OA_GET_TRENDBARS_REQ)
)
