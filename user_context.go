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

	"github.com/coreos/go-oidc/jose"
	"github.com/coreos/go-oidc/oidc"
)

// extractIdentity parse the jwt token and extracts the various elements is order to construct
//
// This is function that concentrates keycloak dependencies (i.e. the structure of the token).
func extractIdentity(token jose.JWT) (*userContext, error) {
	claims, err := token.Claims()
	if err != nil {
		return nil, err
	}
	identity, err := oidc.IdentityFromClaims(claims)
	if err != nil {
		return nil, err
	}

	// @step: ensure we have and can extract the preferred name of the user, if not, we set to the ID
	preferredName, found, err := claims.StringClaim(claimPreferredName)
	if err != nil || !found {
		preferredName = identity.Email
	}

	var audiences []string
	aud, found, err := claims.StringClaim(claimAudience)
	if err == nil && found {
		audiences = append(audiences, aud)
	} else {
		aud, found, erc := claims.StringsClaim(claimAudience)
		if erc != nil || !found {
			return nil, ErrNoTokenAudience
		}
		audiences = aud
	}

	// @step: extract the realm roles
	var roleList []string
	if realmRoles, found := claims[claimRealmAccess].(map[string]interface{}); found {
		if rawRoles, found := realmRoles[claimResourceRoles]; found {
			roles, ok := rawRoles.([]interface{})
			if ok {
				for _, r := range roles {
					roleList = append(roleList, fmt.Sprintf("%s", r))
				}
			}
			// invalid claim is ignored
		}
	}

	// @step: extract the client roles from the access token
	if accesses, found := claims[claimResourceAccess].(map[string]interface{}); found {
		for name, list := range accesses {
			scopes, isMap := list.(map[string]interface{})
			if isMap {
				if roles, found := scopes[claimResourceRoles]; found {
					rolesForKey, isSlice := roles.([]interface{})
					if isSlice {
						for _, r := range rolesForKey {
							roleList = append(roleList, fmt.Sprintf("%s:%s", name, r))
						}
					}
				}
			}
		}
	}

	// @step: extract any group information from the tokens
	groups, _, err := claims.StringsClaim(claimGroups)
	if err != nil {
		return nil, err
	}

	return &userContext{
		audiences:     audiences,
		claims:        claims,
		email:         identity.Email,
		expiresAt:     identity.ExpiresAt,
		groups:        groups,
		id:            identity.ID,
		name:          preferredName,
		preferredName: preferredName,
		roles:         roleList,
		token:         token,
	}, nil
}

// userContext holds the information extracted the token
type userContext struct {
	// the id of the user
	id string
	// the audience for the token
	audiences []string
	// whether the context is from a session cookie or authorization header
	bearerToken bool
	// the claims associated to the token
	claims jose.Claims
	// the email associated to the user
	email string
	// the expiration of the access token
	expiresAt time.Time
	// groups is a collection of groups the user in in
	groups []string
	// a name of the user
	name string
	// preferredName is the name of the user
	preferredName string
	// roles is a collection of roles the users holds
	roles []string
	// the access token itself
	token jose.JWT
}

// isAudience checks the audience
func (r *userContext) isAudience(aud string) bool {
	return containsString(aud, r.audiences)
}

// getRoles returns a list of roles
func (r *userContext) getRoles() string {
	return strings.Join(r.roles, ",")
}

// isExpired checks if the token has expired
func (r *userContext) isExpired() bool {
	return r.expiresAt.Before(time.Now())
}

// isBearer checks if the token
func (r *userContext) isBearer() bool {
	return r.bearerToken
}

// isCookie checks if it's by a cookie
func (r *userContext) isCookie() bool {
	return !r.isBearer()
}

// String returns a string representation of the user context
func (r *userContext) String() string {
	return fmt.Sprintf("user: %s, expires: %s, roles: %s", r.preferredName, r.expiresAt.String(), strings.Join(r.roles, ","))
}
