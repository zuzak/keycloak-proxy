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
	"reflect"
	"testing"
	"time"

	"github.com/gambol99/go-oidc/jose"
	"github.com/stretchr/testify/assert"
)

func newFakeAccessToken() jose.JWT {
	testToken, _ := jose.NewJWT(
		jose.JOSEHeader{
			"alg": "RS256",
		},
		jose.Claims{
			"jti": "4ee75b8e-3ee6-4382-92d4-3390b4b4937b",
			//"exp": "1450372969",
			"nbf":            0,
			"iat":            "1450372669",
			"iss":            "https://keycloak.example.com/auth/realms/commons",
			"aud":            "test",
			"sub":            "1e11e539-8256-4b3b-bda8-cc0d56cddb48",
			"typ":            "Bearer",
			"azp":            "clientid",
			"session_state":  "98f4c3d2-1b8c-4932-b8c4-92ec0ea7e195",
			"client_session": "f0105893-369a-46bc-9661-ad8c747b1a69",
			"resource_access": map[string]interface{}{
				"openvpn": map[string]interface{}{
					"roles": []string{
						"dev-vpn",
					},
				},
			},
			"email":              "gambol99@gmail.com",
			"name":               "Rohith Jayawardene",
			"family_name":        "Jayawardene",
			"preferred_username": "rjayawardene",
			"given_name":         "Rohith",
		},
	)

	return testToken
}

func getFakeRealmAccessToken(t *testing.T) jose.JWT {
	testToken, err := jose.NewJWT(
		jose.JOSEHeader{
			"alg": "RS256",
		},
		jose.Claims{
			"jti": "4ee75b8e-3ee6-4382-92d4-3390b4b4937b",
			//"exp": "1450372969",
			"nbf":            0,
			"iat":            "1450372669",
			"iss":            "https://keycloak.example.com/auth/realms/commons",
			"aud":            "test",
			"sub":            "1e11e539-8256-4b3b-bda8-cc0d56cddb48",
			"typ":            "Bearer",
			"azp":            "clientid",
			"session_state":  "98f4c3d2-1b8c-4932-b8c4-92ec0ea7e195",
			"client_session": "f0105893-369a-46bc-9661-ad8c747b1a69",
			"realm_access": map[string]interface{}{
				"roles": []string{
					"dsp-dev-vpn",
					"vpn-user",
					"dsp-prod-vpn",
				},
			},
			"resource_access": map[string]interface{}{
				"openvpn": map[string]interface{}{
					"roles": []string{
						"dev-vpn",
					},
				},
			},
			"email":              "gambol99@gmail.com",
			"name":               "Rohith Jayawardene",
			"family_name":        "Jayawardene",
			"preferred_username": "rjayawardene",
			"given_name":         "Rohith",
		},
	)
	if err != nil {
		t.Fatalf("unable to generate a token: %s", err)
	}

	return testToken
}

func TestIsAudience(t *testing.T) {
	user := &userContext{
		audience: "test",
	}
	if !user.isAudience("test") {
		t.Errorf("return should not have been false")
	}
	if user.isAudience("test1") {
		t.Errorf("return should not have been true")
	}
}

func TestGetUserRoles(t *testing.T) {
	user := &userContext{
		roles: []string{"1", "2", "3"},
	}
	if user.getRoles() != "1,2,3" {
		t.Errorf("we should have received a true resposne")
	}
	if user.getRoles() == "nothing" {
		t.Errorf("we should have recieved a false response")
	}
}

func TestIsExpired(t *testing.T) {
	user := &userContext{
		expiresAt: time.Now(),
	}
	if !user.isExpired() {
		t.Errorf("we should have been false")
	}
}

func TestIsBearerToken(t *testing.T) {
	user := &userContext{
		bearerToken: true,
	}
	if !user.isBearer() {
		t.Errorf("the bearer token should have been true")
	}
}

func TestGetUserContext(t *testing.T) {
	context, err := extractIdentity(newFakeAccessToken())
	assert.NoError(t, err)
	assert.NotNil(t, context)
	assert.Equal(t, "1e11e539-8256-4b3b-bda8-cc0d56cddb48", context.id)
	assert.Equal(t, "gambol99@gmail.com", context.email)
	assert.Equal(t, "rjayawardene", context.preferredName)
	roles := []string{"openvpn:dev-vpn"}
	if !reflect.DeepEqual(context.roles, roles) {
		t.Errorf("the claims are not the same, %v <-> %v", context.roles, roles)
	}
}

func BenchmarkExtractIdentity(b *testing.B) {
	token := newFakeAccessToken()
	for n := 0; n < b.N; n++ {
		extractIdentity(token)
	}
}

func TestGetUserRealmRoleContext(t *testing.T) {
	context, err := extractIdentity(getFakeRealmAccessToken(t))
	assert.NoError(t, err)
	assert.NotNil(t, context)
	assert.Equal(t, "1e11e539-8256-4b3b-bda8-cc0d56cddb48", context.id)
	assert.Equal(t, "gambol99@gmail.com", context.email)
	assert.Equal(t, "rjayawardene", context.preferredName)
	roles := []string{"dsp-dev-vpn", "vpn-user", "dsp-prod-vpn", "openvpn:dev-vpn"}
	if !reflect.DeepEqual(context.roles, roles) {
		t.Errorf("the claims are not the same, %v <-> %v", context.roles, roles)
	}

}
