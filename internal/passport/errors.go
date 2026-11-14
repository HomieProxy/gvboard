package passport

import "gvboard/internal/httpx"

// 错误信息即翻译键，沿用原项目语言包中的文本。
var (
	errInvalidParams     = httpx.Validation("Invalid parameters")
	errEmailRequired     = httpx.Validation("Email can not be empty")
	errEmailFormat       = httpx.Validation("Email format is incorrect")
	errPasswordRequired  = httpx.Validation("Password can not be empty")
	errPasswordMin       = httpx.Validation("Password must be greater than 8 digits")
	errEmailCodeRequired = httpx.Validation("Email verification code cannot be empty")

	errEmailCode       = httpx.BadRequest("Incorrect email verification code")
	errCaptcha         = httpx.BadRequest("Invalid code is incorrect")
	errWhitelist       = httpx.BadRequest("Email suffix is not in the Whitelist")
	errGmailAlias      = httpx.BadRequest("Gmail alias is not supported")
	errRegisterClosed  = httpx.Forbidden("Registration has closed")
	errInviteRequired  = httpx.BadRequest("You must use the invitation code to register")
	errInviteInvalid   = httpx.BadRequest("Invalid invitation code")
	errEmailExists     = httpx.Conflict("Email already exists")
	errEmailRegistered = httpx.Conflict("This email is registered")
	errNotRegistered   = httpx.BadRequest("This email is not registered in the system")
	errLoginFailed     = httpx.BadRequest("Incorrect email or password")
	errSuspended       = httpx.Forbidden("Your account has been suspended")
	errTokenInvalid    = httpx.BadRequest("Token error")
	errUserNotExist    = httpx.BadRequest("The user does not exist")

	errRegisterLimit = httpx.TooMany("Register frequently, please try again after :minute minute")
	errPasswordLimit = httpx.TooMany("There are too many password errors, please try again after :minute minutes.")
	errResetLimit    = httpx.TooMany("Reset failed, Please try again later")
	errCodeSent      = httpx.TooMany("Email verification code has been sent, please request again later")
	errTooMany       = httpx.TooMany("Too many requests, please try again later.")
)
