package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("expired token")
)

type Claims struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"` // doctor, patient
	DeviceID string `json:"device_id"`
	DeptID   string `json:"dept_id,omitempty"`
	jwt.RegisteredClaims
}

type JWTAuth struct {
	secret     []byte
	expireTime time.Duration
}

func NewJWTAuth(secret string, expireTime time.Duration) *JWTAuth {
	return &JWTAuth{
		secret:     []byte(secret),
		expireTime: expireTime,
	}
}

// GenerateToken 生成 token
func (j *JWTAuth) GenerateToken(userID, role, deviceID, deptID string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Role:     role,
		DeviceID: deviceID,
		DeptID:   deptID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.expireTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(j.secret)
}

func (j *JWTAuth) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return j.secret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}
