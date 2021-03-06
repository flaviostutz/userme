package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/go-gomail/gomail"
)

func sendMail(subject string, htmlBody string, mailTo string, mailToName string) error {
	logrus.Infof("Sending mail %s - %s", mailTo, subject)

	if !strings.HasPrefix(strings.ToLower(htmlBody), "<html>") {
		htmlBody = fmt.Sprintf("<html>%s</html>", htmlBody)
	}

	m := gomail.NewMessage(gomail.SetEncoding("8bit"))
	m.SetAddressHeader("From", opt.mailFromAddress, opt.mailFromName)
	m.SetAddressHeader("To", mailTo, mailToName)
	m.SetHeader("Subject", subject)
	m.SetHeader("Message-ID", fmt.Sprintf("<%s-%s>", uuid.New().String(), opt.mailFromAddress))
	// m.SetAddressHeader("Cc", "dan@example.com", "Dan")
	// m.Attach("/home/Alex/lolcat.jpg")
	m.SetBody("text/html", htmlBody)

	d := gomail.NewDialer(opt.mailSMTPHost, opt.mailSMTPPort, opt.mailSMTPUser, opt.mailSMTPPass)

	return d.DialAndSend(m)
}

func createJWTToken(email string, expirationMinutes int, typ string, authType string, customClaims jwt.MapClaims) (jwt.MapClaims, string, error) {
	sm := jwt.GetSigningMethod(opt.jwtSigningMethod)
	jti := uuid.New()
	claims := jwt.MapClaims{
		"iss":      opt.jwtIssuer,
		"sub":      email,
		"exp":      time.Now().Unix() + int64(60*expirationMinutes),
		"iat":      time.Now().Unix(),
		"nbf":      time.Now().Unix(),
		"jti":      jti.String(),
		"typ":      typ,
		"authType": authType,
	}
	if customClaims != nil {
		for k, v := range customClaims {
			claims[k] = v
		}
	}
	token := jwt.NewWithClaims(sm, claims)
	tokenString, err := token.SignedString(opt.jwtPrivateKey)
	return claims, tokenString, err
}

func loadAuthorizationToken(request *http.Request) (jwt.MapClaims, error) {
	v, exists := request.Header["Authorization"]
	if !exists {
		return nil, fmt.Errorf("Authorization header not found")
	}
	tokenContents := strings.Replace(v[0], "Bearer ", "", 1)
	claims := &jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenContents, claims, func(token *jwt.Token) (interface{}, error) {
		return opt.jwtPublicKey, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("Token is invalid")
	}
	return *claims, nil
}

func claimEquals(claims jwt.MapClaims, claimName string, value string) bool {
	v, exists := claims[claimName]
	if !exists {
		return false
	}
	return v == value
}

func createAccessAndRefreshToken(name string, email string, authType string, accessTokenClaims jwt.MapClaims, refreshTokenClaims jwt.MapClaims) (gin.H, error) {
	accessToken, accessTokenStr, err := createJWTToken(email, opt.accessTokenDefaultExpirationMinutes, "access", authType, accessTokenClaims)
	if err != nil {
		return nil, fmt.Errorf("accessToken err=%s", err)
	}

	refreshToken, refreshTokenStr, err := createJWTToken(email, opt.refreshTokenDefaultExpirationMinutes, "refresh", authType, refreshTokenClaims)
	if err != nil {
		return nil, fmt.Errorf("refreshToken err=%s", err)
	}

	ae, exists := accessToken["exp"].(int64)
	if !exists {
		return nil, fmt.Errorf("exp not found")
	}
	at := time.Unix(ae, 0)

	re, exists := refreshToken["exp"].(int64)
	if !exists {
		return nil, fmt.Errorf("exp not found")
	}
	rt := time.Unix(re, 0)

	return gin.H{
		"email":                  email,
		"name":                   name,
		"accessToken":            accessTokenStr,
		"accessTokenExpiration":  at.Format(time.RFC3339),
		"refreshToken":           refreshTokenStr,
		"refreshTokenExpiration": rt.Format(time.RFC3339),
	}, nil
}

func loadAndValidateToken(req *http.Request, tokenType string, email string) (jwt.MapClaims, error) {
	claims, err := loadAuthorizationToken(req)
	if err != nil {
		return nil, err
	}

	if tokenType != "" && !claimEquals(claims, "typ", tokenType) {
		return nil, fmt.Errorf("Token type is not %s for %s", tokenType, email)
	}

	if email != "" {
		if !claimEquals(claims, "sub", email) {
			return nil, fmt.Errorf("Token sub is not %s", email)
		}
	}

	return claims, nil
}

func requestURLWithJsonResponse(method string, url string, body string, contentType string, customHeaders map[string]string, expectedStatus int) (map[string]interface{}, error) {
	b := strings.NewReader(body)
	req, err := http.NewRequest(method, url, b)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if customHeaders != nil {
		for k, v := range customHeaders {
			req.Header.Set(k, v)
		}
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != expectedStatus {
		rb, _ := ioutil.ReadAll(response.Body)
		return nil, fmt.Errorf("Response for url=%s status=%d body=%s", url, response.StatusCode, string(rb))
	}

	resp := make(map[string]interface{})
	data, _ := ioutil.ReadAll(response.Body)
	err2 := json.Unmarshal(data, &resp)
	if err2 != nil {
		logrus.Debugf("Cannot parse json response. Ignoring.")
	}

	return resp, nil
}
