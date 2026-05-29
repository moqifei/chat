// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ad

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/openimsdk/tools/log"
)

// Config holds the Microsoft AD / LDAP connection configuration.
type Config struct {
	// Enable controls whether AD authentication is active.
	Enable bool `mapstructure:"enable"`
	// ServerURL is the LDAP server address (e.g. ldap://ad.example.com:389 or ldaps://ad.example.com:636).
	ServerURL string `mapstructure:"serverURL"`
	// BaseDN is the base distinguished name for user searches (e.g. DC=example,DC=com).
	BaseDN string `mapstructure:"baseDN"`
	// UserDN is a Go template for building the user DN used during direct bind.
	// The placeholder %s is replaced with the username.
	// Example: "CN=%s,OU=Users,DC=example,DC=com"
	// When empty, search-based authentication will be used (BindDN / BindPassword required).
	UserDN string `mapstructure:"userDN"`
	// BindDN is the service account DN used to search for user entries (e.g. CN=svc,OU=Service,DC=example,DC=com).
	BindDN string `mapstructure:"bindDN"`
	// BindPassword is the password of the service account.
	BindPassword string `mapstructure:"bindPassword"`
	// UserFilter is the LDAP filter used to locate the user during search-based auth (e.g. "(sAMAccountName=%s)").
	UserFilter string `mapstructure:"userFilter"`
	// UsernameAttribute is the LDAP attribute used as the username for OpenIM (default "sAMAccountName").
	UsernameAttribute string `mapstructure:"usernameAttribute"`
	// EmailAttribute is the LDAP attribute used to extract the email address (default "mail").
	EmailAttribute string `mapstructure:"emailAttribute"`
	// DisplayNameAttribute is the LDAP attribute used to extract the display name (default "displayName").
	DisplayNameAttribute string `mapstructure:"displayNameAttribute"`
	// Domain is the Active Directory domain name (NetBIOS or DNS) used for NTLM authentication.
	// When set, NTLM SASL bind is performed — no TLS / STARTTLS required on plain ldap://.
	// Example: "QA" or "qa.bx". Leave empty to use simple bind.
	Domain string `mapstructure:"domain"`
	// InsecureSkipVerify disables TLS certificate verification (use only in development).
	InsecureSkipVerify bool `mapstructure:"insecureSkipVerify"`
	// StartTLS controls whether STARTTLS is negotiated on plain ldap:// connections.
	// Set to false for local/dev OpenLDAP instances that don't support STARTTLS.
	// Defaults to true (STARTTLS is enabled).
	StartTLS bool `mapstructure:"startTLS"`
	// AutoCreateUser controls whether a local chat user account is automatically created
	// on the first successful AD login when no local record exists yet.
	AutoCreateUser bool `mapstructure:"autoCreateUser"`
}

// UserInfo contains the attributes extracted from Active Directory after a successful authentication.
type UserInfo struct {
	Username    string
	DisplayName string
	Email       string
	DN          string
}

// Client wraps an LDAP connection for Microsoft AD authentication.
type Client struct {
	cfg Config
}

// New creates a new AD client with the given configuration.
func New(cfg Config) *Client {
	if cfg.UsernameAttribute == "" {
		cfg.UsernameAttribute = "sAMAccountName"
	}
	if cfg.EmailAttribute == "" {
		cfg.EmailAttribute = "mail"
	}
	if cfg.DisplayNameAttribute == "" {
		cfg.DisplayNameAttribute = "displayName"
	}
	return &Client{cfg: cfg}
}

// Enabled returns whether AD authentication is enabled.
func (c *Client) Enabled() bool {
	return c.cfg.Enable
}

// Authenticate tries to bind against the AD server with the given username and password.
// On success it returns the directory user info.
func (c *Client) Authenticate(username, password string) (*UserInfo, error) {
	log.ZInfo(nil, "ad authenticate start", "username", username, "serverURL", c.cfg.ServerURL, "baseDN", c.cfg.BaseDN, "domain", c.cfg.Domain, "userDN", c.cfg.UserDN)

	conn, err := c.dial()
	if err != nil {
		log.ZError(nil, "ad connect failed", err, "username", username)
		return nil, fmt.Errorf("ad: connect failed: %w", err)
	}
	defer conn.Close()

	var info *UserInfo
	var authMethod string

	// When Domain is set, use NTLM SASL bind — no TLS / STARTTLS needed.
	if c.cfg.Domain != "" {
		authMethod = "NTLM"
		info, err = c.authNTLM(conn, username, password)
	} else if !strings.HasPrefix(c.cfg.ServerURL, "ldaps://") && c.cfg.StartTLS {
		authMethod = "simple+TLS"
		tlsConfig := &tls.Config{InsecureSkipVerify: c.cfg.InsecureSkipVerify}
		if tlsErr := conn.StartTLS(tlsConfig); tlsErr != nil {
			log.ZError(nil, "ad starttls failed", tlsErr, "username", username)
			return nil, fmt.Errorf("ad: starttls failed: %w", tlsErr)
		}
		if c.cfg.UserDN != "" {
			info, err = c.authDirectBind(conn, username, password)
		} else {
			info, err = c.authSearchBind(conn, username, password)
		}
	} else if c.cfg.UserDN != "" {
		authMethod = "directBind"
		info, err = c.authDirectBind(conn, username, password)
	} else {
		authMethod = "searchBind"
		info, err = c.authSearchBind(conn, username, password)
	}

	if err != nil {
		log.ZError(nil, "ad authenticate failed", err, "username", username, "authMethod", authMethod)
		return nil, err
	}

	log.ZInfo(nil, "ad authenticate success", "username", username, "authMethod", authMethod, "adUsername", info.Username, "displayName", info.DisplayName, "email", info.Email, "dn", info.DN)
	return info, nil
}

func (c *Client) dial() (*ldap.Conn, error) {
	if strings.HasPrefix(c.cfg.ServerURL, "ldaps://") && c.cfg.InsecureSkipVerify {
		return ldap.DialURL(c.cfg.ServerURL, ldap.DialWithTLSConfig(&tls.Config{
			InsecureSkipVerify: true,
		}))
	}
	return ldap.DialURL(c.cfg.ServerURL)
}

// authDirectBind builds the full user DN from the UserDN template and tries a single bind.
func (c *Client) authDirectBind(conn *ldap.Conn, username, password string) (*UserInfo, error) {
	userDN := fmt.Sprintf(c.cfg.UserDN, ldap.EscapeFilter(username))

	if err := conn.Bind(userDN, password); err != nil {
		return nil, fmt.Errorf("ad: bind failed for %s: %w", username, err)
	}

	// Fetch attributes so we can return email/display name.
	info, err := c.lookupEntry(conn, userDN)
	if err != nil {
		// Still authenticated – return minimal info.
		return &UserInfo{Username: username, DN: userDN}, nil
	}
	return info, nil
}

// authSearchBind uses a service account to search for the user and then binds as that user.
func (c *Client) authSearchBind(conn *ldap.Conn, username, password string) (*UserInfo, error) {
	// Bind as service account.
	if err := conn.Bind(c.cfg.BindDN, c.cfg.BindPassword); err != nil {
		return nil, fmt.Errorf("ad: service bind failed: %w", err)
	}

	// Search for the user.
	userDN, err := c.searchUserDN(conn, username)
	if err != nil {
		return nil, err
	}

	// Re-bind as the user to verify password.
	if err := conn.Bind(userDN, password); err != nil {
		// If DN bind fails, some AD setups accept UPN (user@domain) instead.
		// Try a fallback bind with UPN when we can derive a domain.
		if upn := c.upnForUser(username); upn != "" {
			if err2 := conn.Bind(upn, password); err2 == nil {
				info, err := c.lookupEntry(conn, userDN)
				if err != nil {
					return &UserInfo{Username: username, DN: userDN}, nil
				}
				return info, nil
			}
		}
		return nil, fmt.Errorf("ad: user bind failed for %s: %w", username, err)
	}

	info, err := c.lookupEntry(conn, userDN)
	if err != nil {
		return &UserInfo{Username: username, DN: userDN}, nil
	}
	return info, nil
}

// authNTLM authenticates via NTLM SASL bind (no TLS / STARTTLS required).
// After the bind, it searches for the authenticated user's attributes.
func (c *Client) authNTLM(conn *ldap.Conn, username, password string) (*UserInfo, error) {
	// NTLM SASL bind — go-ldap handles the entire NTLM negotiate/challenge/response exchange.
	if err := conn.NTLMBind(c.cfg.Domain, username, password); err != nil {
		return nil, fmt.Errorf("ad: ntlm bind failed for %s: %w", username, err)
	}

	// Search for the user's DN so we can look up attributes.
	userDN, err := c.searchUserByAttr(conn, username)
	if err != nil {
		// Authenticated but search failed — return minimal info.
		return &UserInfo{Username: username}, nil
	}

	info, err := c.lookupEntry(conn, userDN)
	if err != nil {
		return &UserInfo{Username: username, DN: userDN}, nil
	}
	return info, nil
}

// searchUserByAttr searches for a user by the configured attribute (e.g. userPrincipalName)
// without needing a separate service account bind.
func (c *Client) searchUserByAttr(conn *ldap.Conn, username string) (string, error) {
	filter := c.cfg.UserFilter
	if filter == "" {
		filter = fmt.Sprintf("(%s=%s)", c.cfg.UsernameAttribute, ldap.EscapeFilter(username))
	} else {
		filter = fmt.Sprintf(filter, ldap.EscapeFilter(username))
	}

	req := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		filter,
		[]string{"dn"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return "", fmt.Errorf("ad: search failed: %w", err)
	}
	if len(result.Entries) == 0 {
		return "", fmt.Errorf("ad: user %q not found in directory", username)
	}
	if len(result.Entries) > 1 {
		return "", fmt.Errorf("ad: multiple entries found for user %q", username)
	}
	return result.Entries[0].DN, nil
}

func (c *Client) searchUserDN(conn *ldap.Conn, username string) (string, error) {
	filter := c.cfg.UserFilter
	if filter == "" {
		filter = fmt.Sprintf("(%s=%s)", c.cfg.UsernameAttribute, ldap.EscapeFilter(username))
	} else {
		filter = fmt.Sprintf(filter, ldap.EscapeFilter(username))
	}

	req := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		filter,
		[]string{"dn"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return "", fmt.Errorf("ad: search failed: %w", err)
	}
	if len(result.Entries) == 0 {
		return "", fmt.Errorf("ad: user %q not found in directory", username)
	}
	if len(result.Entries) > 1 {
		return "", fmt.Errorf("ad: multiple entries found for user %q", username)
	}
	return result.Entries[0].DN, nil
}

func (c *Client) lookupEntry(conn *ldap.Conn, dn string) (*UserInfo, error) {
	attrs := []string{c.cfg.UsernameAttribute, c.cfg.EmailAttribute, c.cfg.DisplayNameAttribute}

	log.ZDebug(nil, "ad lookupEntry start", "dn", dn, "requestedAttrs", attrs)

	req := ldap.NewSearchRequest(
		dn,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(objectClass=*)",
		attrs,
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		log.ZError(nil, "ad attribute lookup failed", err, "dn", dn)
		return nil, fmt.Errorf("ad: attribute lookup failed: %w", err)
	}
	if len(result.Entries) == 0 {
		log.ZWarn(nil, "ad entry not found for DN", nil, "dn", dn)
		return nil, fmt.Errorf("ad: entry not found for DN %q", dn)
	}
	entry := result.Entries[0]

	// Log raw AD entry with all attributes for debugging nickname issues.
	rawAttrs := make(map[string][]string)
	for _, attr := range entry.Attributes {
		rawAttrs[attr.Name] = attr.Values
	}
	rawJSON, _ := json.Marshal(rawAttrs)
	log.ZInfo(nil, "ad raw entry attributes", "dn", dn, "attributes", string(rawJSON))

	info := &UserInfo{
		Username:    entry.GetAttributeValue(c.cfg.UsernameAttribute),
		Email:       entry.GetAttributeValue(c.cfg.EmailAttribute),
		DisplayName: entry.GetAttributeValue(c.cfg.DisplayNameAttribute),
		DN:          dn,
	}
	if info.Username == "" {
		info.Username = entry.GetAttributeValue("sAMAccountName")
		log.ZDebug(nil, "ad username fallback to sAMAccountName", "username", info.Username)
	}

	log.ZInfo(nil, "ad parsed userinfo", "username", info.Username, "displayName", info.DisplayName, "email", info.Email, "dn", info.DN)
	return info, nil
}

// upnForUser attempts to construct a UPN (user@domain) for the given username.
// It prefers an explicit Domain config, then a UPN-style BindDN, then derives
// the domain from the BaseDN's DC components.
func (c *Client) upnForUser(username string) string {
	if username == "" {
		return ""
	}
	// 1) explicit domain config
	if c.cfg.Domain != "" {
		return fmt.Sprintf("%s@%s", username, c.cfg.Domain)
	}
	// 2) bindDN might already be a UPN (user@domain)
	if strings.Contains(c.cfg.BindDN, "@") {
		parts := strings.SplitN(c.cfg.BindDN, "@", 2)
		if len(parts) == 2 && parts[1] != "" {
			return fmt.Sprintf("%s@%s", username, parts[1])
		}
	}
	// 3) derive from BaseDN (DC components -> domain)
	if domain := domainFromBaseDN(c.cfg.BaseDN); domain != "" {
		return fmt.Sprintf("%s@%s", username, domain)
	}
	return ""
}

// domainFromBaseDN extracts a DNS-style domain from a BaseDN by joining DC= parts.
func domainFromBaseDN(baseDN string) string {
	if baseDN == "" {
		return ""
	}
	var parts []string
	for _, p := range strings.Split(baseDN, ",") {
		p = strings.TrimSpace(p)
		up := strings.ToUpper(p)
		if strings.HasPrefix(up, "DC=") {
			parts = append(parts, strings.TrimSpace(p[3:]))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ".")
}
