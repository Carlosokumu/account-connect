package messagevalidator

import (
	"account-connect/internal/messages"

	"github.com/go-playground/validator/v10"
)

type AccountConnectMessageValidator struct {
	validator *validator.Validate
}

func New() *AccountConnectMessageValidator {
	return &AccountConnectMessageValidator{
		validator: validator.New(),
	}
}

// RegisterValidations registers custom validation rules for AccountConnectMessage.
// It adds the following validation rules:
//   - "messagetype_enum": Validates that a MessageType field contains one of the supported message types
//   - "platform_enum": Validates that a Platform field contains one of the supported trading platforms
func (msgvalidator *AccountConnectMessageValidator) RegisterValidations() {
	msgvalidator.validator.RegisterValidation("messagetype_enum", func(fl validator.FieldLevel) bool {
		mt, ok := fl.Field().Interface().(messages.MessageType)
		if !ok {
			return false
		}
		switch mt {
		case
			messages.TypeConnect,
			messages.TypeAuthorizeAccount,
			messages.TypeTraderInfo,
			messages.TypeHistorical,
			messages.TypeAccountSymbols,
			messages.TypeTrendBars,
			messages.TypeError,
			messages.TypeDisconnect,
			messages.TypeStream:
			return true
		default:
			return false
		}
	})

	msgvalidator.validator.RegisterValidation("platform_enum", func(fl validator.FieldLevel) bool {
		mt, ok := fl.Field().Interface().(messages.Platform)
		if !ok {
			return false
		}
		switch mt {
		case
			messages.Ctrader,
			messages.Binance:
			return true
		default:
			return false
		}
	})
}

// Validate performs validation of the given struct using the registered validation rules.
// It wraps the validator.Struct() method to validate all fields of the input struct
// according to the struct tags and custom validations registered with RegisterValidations().
func (msgvalidator *AccountConnectMessageValidator) Validate(i interface{}) error {
	return msgvalidator.validator.Struct(i)
}
