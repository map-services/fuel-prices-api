package models

type AuthRequest struct {
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type AuthResponse struct {
	Success bool      `json:"success"`
	Data    TokenData `json:"data,omitempty"`
	Message string    `json:"message,omitempty"`
}

type TokenData struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	RefreshTokenExpiresIn int `json:"refresh_token_expires_in,omitempty"`
}

type TokenRefreshRequest struct {
	ClientId     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
}
