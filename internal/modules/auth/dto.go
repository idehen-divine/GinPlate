package auth

type SignupDTO struct {
	Name     string `json:"name" binding:"required,max=255"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

type LoginDTO struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,max=72"`
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type RefreshDTO struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type ForgotDTO struct {
	Email string `json:"email" binding:"required,email"`
}

type ResetDTO struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

type CheckDTO struct {
	Token string `json:"token" binding:"required"`
}
