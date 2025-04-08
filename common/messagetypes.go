package common

import messages "account-connect/gen"

var (
	AppAuthMsgType     = uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_REQ)
	AccountAuthMsgType = uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_REQ)
)
