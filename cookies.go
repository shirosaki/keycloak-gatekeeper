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
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SameSite cookie config options
const (
	SameSiteStrict = "Strict"
	SameSiteLax    = "Lax"
	SameSiteNone   = "None"
)

// dropCookie drops a cookie into the response
func (r *oauthProxy) dropCookie(w http.ResponseWriter, host, name, value string, duration time.Duration) {
	cookie := r.cookieDropper(host, name, value, duration)
	http.SetCookie(w, cookie)
}

func (r *oauthProxy) makeCookieDropper() func(string, string, string, time.Duration) *http.Cookie {
	// cookieDropper parses the configuration and delivers a fast cookie setter:
	// config is evaluated only once

	baseCookie := &http.Cookie{
		Domain:   r.config.CookieDomain,
		HttpOnly: r.config.HTTPOnlyCookie,
		Path:     "/",
		Secure:   r.config.SecureCookie,
	}

	switch r.config.SameSiteCookie {
	case SameSiteStrict:
		baseCookie.SameSite = http.SameSiteStrictMode
	case SameSiteLax:
		baseCookie.SameSite = http.SameSiteLaxMode
	}

	makeBase := func(name, value string) *http.Cookie {
		cookie := *baseCookie
		cookie.Name = name
		cookie.Value = value
		return &cookie
	}

	switch {
	case r.config.CookieDomain == "" && r.config.EnableSessionCookies:
		return func(host, name, value string, duration time.Duration) *http.Cookie {
			cookie := makeBase(name, value)
			cookie.Domain = strings.Split(host, ":")[0]
			if duration < 0 {
				cookie.Expires = time.Now().Add(duration)
			}
			return cookie
		}
	case r.config.CookieDomain == "" && !r.config.EnableSessionCookies:
		return func(host, name, value string, duration time.Duration) *http.Cookie {
			cookie := makeBase(name, value)
			cookie.Domain = strings.Split(host, ":")[0]
			if duration != 0 {
				cookie.Expires = time.Now().Add(duration)
			}
			return cookie
		}
	case r.config.CookieDomain != "" && r.config.EnableSessionCookies:
		return func(_, name, value string, duration time.Duration) *http.Cookie {
			cookie := makeBase(name, value)
			if duration < 0 {
				cookie.Expires = time.Now().Add(duration)
			}
			return cookie
		}
	case r.config.CookieDomain != "" && !r.config.EnableSessionCookies:
		return func(host, name, value string, duration time.Duration) *http.Cookie {
			cookie := makeBase(name, value)
			if duration != 0 {
				cookie.Expires = time.Now().Add(duration)
			}
			return cookie
		}
	default:
		panic("dev error guard")
	}
}

const (
	// taking a conservative margin for cases such as safari
	cookieMargin          = 12 + len("set-cookie: ") + 3
	baseCookieChunkLength = 4096 - cookieMargin
)

// maxCookieChunkSize calculates max cookie chunk size, which can be used for cookie value
func (r *oauthProxy) getMaxCookieChunkLength(req *http.Request, cookieName string) int {
	return r.cookieChunker(req.Host, cookieName)
}

func (r *oauthProxy) makeCookieChunker() func(string, string) int {
	// chunkLengthCalculator parses the configuration and delivers a fast calculator:
	// config is evaluated only once
	maxCookieChunkLength := baseCookieChunkLength - len("; Path=/")
	if r.config.HTTPOnlyCookie {
		maxCookieChunkLength -= len("HttpOnly; ")
	}
	if !r.config.EnableSessionCookies {
		maxCookieChunkLength -= len("Expires=Mon, 02 Jan 2006 03:04:05 MST; ")
	}
	if r.config.SameSiteCookie != "" {
		maxCookieChunkLength -= len("SameSite=" + r.config.SameSiteCookie + "; ")
	}
	if r.config.SecureCookie {
		maxCookieChunkLength -= len("Secure")
	}
	if r.config.CookieDomain != "" {
		maxCookieChunkLength -= len("Domain=; ")
		maxCookieChunkLength -= len(r.config.CookieDomain)
		return func(_, cookieName string) int {
			return maxCookieChunkLength - len(cookieName)
		}
	}
	return func(host, cookieName string) int {
		return maxCookieChunkLength - len(cookieName) - len(strings.Split(host, ":")[0])
	}
}

// dropCookieWithChunks drops a cookie from the response, taking into account possible chunks
func (r *oauthProxy) dropCookieWithChunks(req *http.Request, w http.ResponseWriter, name, value string, duration time.Duration) {
	maxCookieChunkLength := r.getMaxCookieChunkLength(req, name)
	if len(value) <= maxCookieChunkLength {
		r.dropCookie(w, req.Host, name, value, duration)
		return
	}
	// write divided cookies because payload is too long for single cookie
	r.dropCookie(w, req.Host, name, value[0:maxCookieChunkLength], duration)
	for i := maxCookieChunkLength; i < len(value); i += maxCookieChunkLength {
		end := i + maxCookieChunkLength
		if end > len(value) {
			end = len(value)
		}
		r.dropCookie(w, req.Host, name+"-"+strconv.Itoa(i/maxCookieChunkLength), value[i:end], duration)
	}
}

// dropAccessTokenCookie drops a access token cookie from the response
func (r *oauthProxy) dropAccessTokenCookie(req *http.Request, w http.ResponseWriter, value string, duration time.Duration) {
	r.dropCookieWithChunks(req, w, r.config.CookieAccessName, value, duration)
}

// dropRefreshTokenCookie drops a refresh token cookie from the response
func (r *oauthProxy) dropRefreshTokenCookie(req *http.Request, w http.ResponseWriter, value string, duration time.Duration) {
	r.dropCookieWithChunks(req, w, r.config.CookieRefreshName, value, duration)
}

// writeStateParameterCookie sets a state parameter cookie into the response
func (r *oauthProxy) writeStateParameterCookie(req *http.Request, w http.ResponseWriter) string {
	uuid := uuid.NewString()
	requestURI := base64.StdEncoding.EncodeToString([]byte(req.URL.RequestURI()))
	r.dropCookie(w, req.Host, requestURICookie, requestURI, 0)
	r.dropCookie(w, req.Host, requestStateCookie, uuid, 0)

	return uuid
}

// clearAllCookies is just a helper function for the below
func (r *oauthProxy) clearAllCookies(req *http.Request, w http.ResponseWriter) {
	r.clearAccessTokenCookie(req, w)
	r.clearRefreshTokenCookie(req, w)
	r.clearStateCookie(req, w)
}

// clearRefreshSessionCookie clears the session cookie
func (r *oauthProxy) clearRefreshTokenCookie(req *http.Request, w http.ResponseWriter) {
	r.dropCookie(w, req.Host, r.config.CookieRefreshName, "", -10*time.Hour)
	r.clearDividedCookies(req, w, r.config.CookieRefreshName)
}

// clearAccessTokenCookie clears the session cookie
func (r *oauthProxy) clearAccessTokenCookie(req *http.Request, w http.ResponseWriter) {
	r.dropCookie(w, req.Host, r.config.CookieAccessName, "", -10*time.Hour)
	r.clearDividedCookies(req, w, r.config.CookieAccessName)
}

// clearStateCookie clears the session state cookie
func (r *oauthProxy) clearStateCookie(req *http.Request, w http.ResponseWriter) {
	r.dropCookie(w, req.Host, requestStateCookie, "", -10*time.Hour)
	r.clearDividedCookies(req, w, requestStateCookie)
}

func (r *oauthProxy) clearDividedCookies(req *http.Request, w http.ResponseWriter, name string) {
	// clear divided cookies
	for i := 1; i < len(req.Cookies()); i++ {
		var _, err = req.Cookie(name + "-" + strconv.Itoa(i))
		if err != nil {
			break
		}
		r.dropCookie(w, req.Host, name+"-"+strconv.Itoa(i), "", -10*time.Hour)
	}
}

// filterCookies is responsible for censoring any cookies we don't want sent
func filterCookies(req *http.Request, filter []string) error {
	// @NOTE: there doesn't appear to be a way of removing a cookie from the http.Request as
	// AddCookie() just append
	cookies := req.Cookies()
	// @step: empty the current cookies
	req.Header.Set("Cookie", "")
	// @step: iterate the cookies and filter out anything we
	for _, x := range cookies {
		var found bool
		// @step: does this cookie match our filter?
		for _, n := range filter {
			if strings.HasPrefix(x.Name, n) {
				req.AddCookie(&http.Cookie{Name: x.Name, Value: "redacted"})
				found = true
				break
			}
		}
		if !found {
			req.AddCookie(x)
		}
	}

	return nil
}
