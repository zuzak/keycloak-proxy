/*
Copyright 2015 All rights reserved.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/gambol99/go-oidc/jose"
	"github.com/gin-gonic/gin"
	"github.com/unrolled/secure"
)

const (
	// cxEnforce is the tag name for a request requiring
	cxEnforce = "Enforcing"
)

//
// loggingHandler is a custom http logger
//
func (r *oauthProxy) loggingHandler() gin.HandlerFunc {
	return func(cx *gin.Context) {
		start := time.Now()
		cx.Next()

		latency := time.Now().Sub(start)

		log.WithFields(log.Fields{
			"client_ip": cx.ClientIP(),
			"method":    cx.Request.Method,
			"status":    cx.Writer.Status(),
			"bytes":     cx.Writer.Size(),
			"path":      cx.Request.URL.Path,
			"latency":   latency.String(),
		}).Infof("[%d] |%s| |%10v| %-5s %s", cx.Writer.Status(), cx.ClientIP(), latency, cx.Request.Method, cx.Request.URL.Path)
	}
}

//
// entryPointHandler checks to see if the request requires authentication
//
func (r oauthProxy) entryPointHandler() gin.HandlerFunc {
	return func(cx *gin.Context) {
		if strings.HasPrefix(cx.Request.URL.Path, oauthURL) {
			cx.Next()
			return
		}

		// step: check if authentication is required - gin doesn't support wildcard url, so we have have to use prefixes
		for _, resource := range r.config.Resources {
			if strings.HasPrefix(cx.Request.URL.Path, resource.URL) {
				if resource.WhiteListed {
					break
				}
				// step: inject the resource into the context, saves us from doing this again
				if containedIn("ANY", resource.Methods) || containedIn(cx.Request.Method, resource.Methods) {
					cx.Set(cxEnforce, resource)
				}
				break
			}
		}
		// step: pass into the authentication, admission and proxy handlers
		cx.Next()
	}
}

//
// authenticationHandler is responsible for verifying the access token
//
func (r *oauthProxy) authenticationHandler() gin.HandlerFunc {
	return func(cx *gin.Context) {
		// step: is authentication required on this uri?
		if _, found := cx.Get(cxEnforce); !found {
			log.Debugf("skipping the authentication handler, resource not protected")
			cx.Next()
			return
		}

		// step: grab the user identity from the request
		user, err := r.getIdentity(cx)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Errorf("no session found in request, redirecting for authorization")

			r.redirectToAuthorization(cx)
			return
		}

		// step: inject the user into the context
		cx.Set(userContextName, user)

		// step: verify the access token
		if r.config.SkipTokenVerification {
			log.Warnf("skip token verification enabled, skipping verification process - FOR TESTING ONLY")

			if user.isExpired() {
				log.WithFields(log.Fields{
					"username":   user.name,
					"expired_on": user.expiresAt.String(),
				}).Errorf("the session has expired and verification switch off")

				r.redirectToAuthorization(cx)
			}

			return
		}

		// step: verify the access token
		if err := verifyToken(r.client, user.token); err != nil {

			// step: if the error post verification is anything other than a token expired error
			// we immediately throw an access forbidden - as there is something messed up in the token
			if err != ErrAccessTokenExpired {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Errorf("verification of the access token failed")

				r.accessForbidden(cx)
				return
			}

			// step: are we refreshing the access tokens?
			if !r.config.EnableRefreshTokens {
				log.WithFields(log.Fields{
					"email":      user.name,
					"expired_on": user.expiresAt.String(),
				}).Errorf("the session has expired and access token refreshing is disabled")

				r.redirectToAuthorization(cx)
				return
			}

			// step: we do not refresh bearer token requests
			if user.isBearer() {
				log.WithFields(log.Fields{
					"email":      user.name,
					"expired_on": user.expiresAt.String(),
				}).Errorf("the session has expired and we are using bearer tokens")

				r.redirectToAuthorization(cx)
				return
			}

			log.WithFields(log.Fields{
				"email":     user.email,
				"client_ip": cx.ClientIP(),
			}).Infof("the accces token for user: %s has expired, attemping to refresh the token", user.email)

			// step: check if the user has refresh token
			rToken, err := r.retrieveRefreshToken(cx, user)
			if err != nil {
				log.WithFields(log.Fields{
					"email": user.email,
					"error": err.Error(),
				}).Errorf("unable to find a refresh token for the client: %s", user.email)

				r.redirectToAuthorization(cx)
				return
			}

			log.WithFields(log.Fields{
				"email": user.email,
			}).Infof("found a refresh token, attempting to refresh access token for user: %s", user.email)

			// step: attempts to refresh the access token
			token, expires, err := getRefreshedToken(r.client, rToken)
			if err != nil {
				// step: has the refresh token expired
				switch err {
				case ErrRefreshTokenExpired:
					log.WithFields(log.Fields{"token": token}).Warningf("the refresh token has expired")
					r.clearAllCookies(cx)
				default:
					log.WithFields(log.Fields{"error": err.Error()}).Errorf("failed to refresh the access token")
				}

				r.redirectToAuthorization(cx)
				return
			}

			// step: inject the refreshed access token
			log.WithFields(log.Fields{
				"email":             user.email,
				"access_expires_in": expires.Sub(time.Now()).String(),
			}).Infof("injecting refreshed access token, expires on: %s", expires.Format(time.RFC1123))

			// step: clear the cookie up
			r.dropAccessTokenCookie(cx, token.Encode(), r.config.IdleDuration)

			if r.useStore() {
				go func(t jose.JWT, rt string) {
					// step: the access token has been updated, we need to delete old reference and update the store
					if err := r.DeleteRefreshToken(t); err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Errorf("unable to delete the old refresh tokem from store")
					}

					// step: store the new refresh token reference place the session in the store
					if err := r.StoreRefreshToken(t, rt); err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Errorf("failed to place the refresh token in the store")

						return
					}
				}(user.token, rToken)
			} else {
				// step: update the expiration on the refresh token
				r.dropRefreshTokenCookie(cx, rToken, r.config.IdleDuration*2)
			}

			// step: update the with the new access token
			user.token = token

			// step: inject the user into the context
			cx.Set(userContextName, user)
		}

		cx.Next()
	}
}

//
// admissionHandler is responsible checking the access token against the protected resource
//
func (r *oauthProxy) admissionHandler() gin.HandlerFunc {
	// step: compile the regex's for the claims
	claimMatches := make(map[string]*regexp.Regexp, 0)
	for k, v := range r.config.MatchClaims {
		claimMatches[k] = regexp.MustCompile(v)
	}

	return func(cx *gin.Context) {
		// step: if authentication is required on this, grab the resource spec
		ur, found := cx.Get(cxEnforce)
		if !found {
			return
		}

		// step: grab the identity from the context
		uc, found := cx.Get(userContextName)
		if !found {
			panic("there is no identity in the request context")
		}

		resource := ur.(*Resource)
		user := uc.(*userContext)

		// step: check the audience for the token is us
		if r.config.ClientID != "" && !user.isAudience(r.config.ClientID) {
			log.WithFields(log.Fields{
				"username":   user.name,
				"expired_on": user.expiresAt.String(),
				"issued":     user.audience,
				"clientid":   r.config.ClientID,
			}).Warnf("the access token audience is not us, redirecting back for authentication")

			r.accessForbidden(cx)
			return
		}

		// step: we need to check the roles
		if roles := len(resource.Roles); roles > 0 {
			if !hasRoles(resource.Roles, user.roles) {
				log.WithFields(log.Fields{
					"access":   "denied",
					"username": user.name,
					"resource": resource.URL,
					"required": resource.GetRoles(),
				}).Warnf("access denied, invalid roles")

				r.accessForbidden(cx)
				return
			}
		}

		// step: if we have any claim matching, validate the tokens has the claims
		for claimName, match := range claimMatches {
			// step: if the claim is NOT in the token, we access deny
			value, found, err := user.claims.StringClaim(claimName)
			if err != nil {
				log.WithFields(log.Fields{
					"access":   "denied",
					"username": user.name,
					"resource": resource.URL,
					"error":    err.Error(),
				}).Errorf("unable to extract the claim from token")

				r.accessForbidden(cx)
				return
			}

			if !found {
				log.WithFields(log.Fields{
					"access":   "denied",
					"username": user.name,
					"resource": resource.URL,
					"claim":    claimName,
				}).Warnf("the token does not have the claim")

				r.accessForbidden(cx)
				return
			}

			// step: check the claim is the same
			if !match.MatchString(value) {
				log.WithFields(log.Fields{
					"access":   "denied",
					"username": user.name,
					"resource": resource.URL,
					"claim":    claimName,
					"issued":   value,
					"required": match,
				}).Warnf("the token claims does not match claim requirement")

				r.accessForbidden(cx)
				return
			}
		}

		log.WithFields(log.Fields{
			"access":   "permitted",
			"username": user.name,
			"resource": resource.URL,
			"expires":  user.expiresAt.Sub(time.Now()).String(),
		}).Debugf("resource access permitted: %s", cx.Request.RequestURI)
	}
}

//
// crossOriginResourceHandler injects the CORS headers, if set, for request made to /oauth
//
func (r *oauthProxy) crossOriginResourceHandler(c CORS) gin.HandlerFunc {
	return func(cx *gin.Context) {
		if len(c.Origins) > 0 {
			cx.Writer.Header().Set("Access-Control-Allow-Origin", strings.Join(c.Origins, ","))
		}
		if len(c.Methods) > 0 {
			cx.Writer.Header().Set("Access-Control-Allow-Methods", strings.Join(c.Methods, ","))
		}
		if len(c.Headers) > 0 {
			cx.Writer.Header().Set("Access-Control-Allow-Headers", strings.Join(c.Headers, ","))
		}
		if len(c.ExposedHeaders) > 0 {
			cx.Writer.Header().Set("Access-Control-Expose-Headers", strings.Join(c.ExposedHeaders, ","))
		}
		if c.Credentials {
			cx.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if c.MaxAge > 0 {
			cx.Writer.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", int(c.MaxAge.Seconds())))
		}
	}
}

//
// upstreamHeadersHandler is responsible for add the authentication headers for the upstream
//
func (r *oauthProxy) upstreamHeadersHandler(custom []string) gin.HandlerFunc {
	// step: we don't wanna do this every time, quicker to perform once
	customClaims := make(map[string]string)
	for _, x := range custom {
		customClaims[x] = fmt.Sprintf("X-Auth-%s", toHeader(x))
	}

	return func(cx *gin.Context) {
		// step: add a custom headers to the request
		for k, v := range r.config.Headers {
			cx.Request.Header.Add(k, v)
		}

		// step: retrieve the user context if any
		if user, found := cx.Get(userContextName); found {
			id := user.(*userContext)
			cx.Request.Header.Add("X-Auth-Userid", id.name)
			cx.Request.Header.Add("X-Auth-Subject", id.id)
			cx.Request.Header.Add("X-Auth-Username", id.name)
			cx.Request.Header.Add("X-Auth-Email", id.email)
			cx.Request.Header.Add("X-Auth-ExpiresIn", id.expiresAt.String())
			cx.Request.Header.Add("X-Auth-Token", id.token.Encode())
			cx.Request.Header.Add("X-Auth-Roles", strings.Join(id.roles, ","))
			cx.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", id.token.Encode()))

			// step: inject any custom claims
			for claim, header := range customClaims {
				if claim, found := id.claims[claim]; found {
					cx.Request.Header.Add(header, fmt.Sprintf("%v", claim))
				}
			}
		}
		// step: add the default headers
		cx.Request.Header.Add("X-Forwarded-For", cx.Request.RemoteAddr)
		cx.Request.Header.Set("X-Forwarded-Agent", prog)
		cx.Request.Header.Set("X-Forwarded-Host", cx.Request.Host)
	}
}

//
// securityHandler performs numerous security checks on the request
//
func (r *oauthProxy) securityHandler() gin.HandlerFunc {
	// step: create the security options
	secure := secure.New(secure.Options{
		AllowedHosts:       r.config.Hostnames,
		BrowserXssFilter:   true,
		ContentTypeNosniff: true,
		FrameDeny:          true,
	})

	return func(cx *gin.Context) {
		// step: pass through the security middleware
		if err := secure.Process(cx.Writer, cx.Request); err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Errorf("failed security middleware")

			cx.Abort()
			return
		}

		// step: permit the request to continue
		cx.Next()
	}
}
