package auth

type SignupDTO struct {
	Name     string `json:"name" binding:"required,max=255"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginDTO struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
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

type CheckDTO struct {
	Token string `json:"token" binding:"required"`
}
