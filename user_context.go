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
	"strings"
	"time"

	"github.com/gambol99/go-oidc/jose"
	"github.com/gambol99/go-oidc/oidc"
)

//
// userContext represents a user
//
type userContext struct {
	// the id of the user
	id string
	// the email associated to the user
	email string
	// a name of the user
	name string
	// the preferred name
	preferredName string
	// the expiration of the access token
	expiresAt time.Time
	// a set of roles associated
	roles []string
	// the audience for the token
	audience string
	// the access token itself
	token jose.JWT
	// the claims associated to the token
	claims jose.Claims
	// whether the context is from a session cookie or authorization header
	bearerToken bool
}

//
// extractIdentity parse the jwt token and extracts the various elements is order to construct
//
func extractIdentity(token jose.JWT) (*userContext, error) {
	// step: decode the claims from the tokens
	claims, err := token.Claims()
	if err != nil {
		return nil, err
	}

	// step: extract the identity
	identity, err := oidc.IdentityFromClaims(claims)
	if err != nil {
		return nil, err
	}

	// step: ensure we have and can extract the preferred name of the user, if not, we set to the ID
	preferredName, found, err := claims.StringClaim(claimPreferredName)
	if err != nil || !found {
		// choice: set the preferredName to the Email if claim not found
		preferredName = identity.Email
	}

	// step: retrieve the audience from access token
	audience, found, err := claims.StringClaim(claimAudience)
	if err != nil || !found {
		return nil, ErrNoTokenAudience
	}
	var list []string

	// step: extract the realm roles
	if realmRoles, found := claims[claimRealmAccess].(map[string]interface{}); found {
		if roles, found := realmRoles[claimResourceRoles]; found {
			for _, r := range roles.([]interface{}) {
				list = append(list, fmt.Sprintf("%s", r))
			}
		}
	}

	// step: extract the roles from the access token
	if accesses, found := claims[claimResourceAccess].(map[string]interface{}); found {
		for roleName, roleList := range accesses {
			scopes := roleList.(map[string]interface{})
			if roles, found := scopes[claimResourceRoles]; found {
				for _, r := range roles.([]interface{}) {
					list = append(list, fmt.Sprintf("%s:%s", roleName, r))
				}
			}
		}
	}

	return &userContext{
		id:            identity.ID,
		name:          preferredName,
		audience:      audience,
		preferredName: preferredName,
		email:         identity.Email,
		expiresAt:     identity.ExpiresAt,
		roles:         list,
		token:         token,
		claims:        claims,
	}, nil
}

//
// isAudience checks the audience
//
func (r userContext) isAudience(aud string) bool {
	if r.audience == aud {
		return true
	}

	return false
}

//
// getRoles returns a list of roles
//
func (r userContext) getRoles() string {
	return strings.Join(r.roles, ",")
}

//
// isExpired checks if the token has expired
//
func (r userContext) isExpired() bool {
	return r.expiresAt.Before(time.Now())
}

//
// isBearerToken checks if the token
//
func (r userContext) isBearer() bool {
	return r.bearerToken
}

//
// String returns a string representation of the user context
//
func (r userContext) String() string {
	return fmt.Sprintf("user: %s, expires: %s, roles: %s", r.preferredName, r.expiresAt.String(), strings.Join(r.roles, ","))
}
