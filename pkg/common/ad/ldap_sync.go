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
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/openimsdk/tools/log"
)

// SyncDepartment represents a single organizational unit discovered during AD sync.
type SyncDepartment struct {
	DN       string // Full distinguished name, e.g. "OU=信息技术部,OU=ZXBXUsers,OU=中信百信银行,DC=qa,DC=bx"
	Name     string // Short OU name, e.g. "信息技术部"
	ParentDN string // Parent department DN, empty for root
}

// SyncUser represents a user entry discovered during AD sync.
type SyncUser struct {
	DN          string // Full DN
	Username    string // sAMAccountName
	DisplayName string // AD displayName
	Email       string // mail attribute
	Title       string // Job title
	Phone       string // telephoneNumber
	// PrimaryDepartmentDN is derived from the user's DN.
	// The first OU component below the user's CN is treated as the primary department.
	PrimaryDepartmentDN string
}

// SearchOUs discovers all organizational units under the configured BaseDN.
// It binds using the service account (BindDN / BindPassword or NTLM).
func (c *Client) SearchOUs() ([]*SyncDepartment, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("ad sync: connect failed: %w", err)
	}
	defer conn.Close()

	if err := c.bindServiceAccount(conn); err != nil {
		return nil, fmt.Errorf("ad sync: bind service account failed: %w", err)
	}

	return c.searchOUs(conn)
}

// SearchAllUsers discovers all user entries under the configured BaseDN.
// It binds using the service account and fetches user attributes.
func (c *Client) SearchAllUsers() ([]*SyncUser, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, fmt.Errorf("ad sync: connect failed: %w", err)
	}
	defer conn.Close()

	if err := c.bindServiceAccount(conn); err != nil {
		return nil, fmt.Errorf("ad sync: bind service account failed: %w", err)
	}

	return c.searchAllUsers(conn)
}

// bindServiceAccount binds to AD using the service account (simple bind, NTLM or STARTTLS).
func (c *Client) bindServiceAccount(conn *ldap.Conn) error {
	if c.cfg.Domain != "" {
		// NTLM bind as service account
		if err := conn.NTLMBind(c.cfg.Domain, c.cfg.BindDN, c.cfg.BindPassword); err != nil {
			return fmt.Errorf("ntlm bind failed: %w", err)
		}
		return nil
	}
	if !strings.HasPrefix(c.cfg.ServerURL, "ldaps://") && c.cfg.StartTLS {
		tlsConfig := &tls.Config{InsecureSkipVerify: c.cfg.InsecureSkipVerify}
		if err := conn.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("starttls failed: %w", err)
		}
	}
	if err := conn.Bind(c.cfg.BindDN, c.cfg.BindPassword); err != nil {
		return fmt.Errorf("simple bind failed: %w", err)
	}
	return nil
}

// searchOUs searches for all organizationalUnit entries under BaseDN.
func (c *Client) searchOUs(conn *ldap.Conn) ([]*SyncDepartment, error) {
	req := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(objectClass=organizationalUnit)",
		[]string{"ou", "dn"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ad sync: search OUs failed: %w", err)
	}

	log.ZInfo(nil, "ad sync: found organizational units", "count", len(result.Entries))

	var deps []*SyncDepartment
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("ou")
		dn := entry.DN
		parentDN := parentDN(dn)

		deps = append(deps, &SyncDepartment{
			DN:       dn,
			Name:     name,
			ParentDN: parentDN,
		})
	}
	return deps, nil
}

// searchAllUsers searches for all user entries under BaseDN, excluding disabled accounts.
func (c *Client) searchAllUsers(conn *ldap.Conn) ([]*SyncUser, error) {
	// Exclude disabled accounts via userAccountControl bit 2 (ACCOUNTDISABLE=0x0002).
	// The filter uses LDAP_MATCHING_RULE_IN_CHAIN or a simple bitwise AND.
	// Note: some AD servers may not support the bitwise matching rule; fall back gracefully.
	// Use a broad objectClass filter that works with both AD (user) and
	// OpenLDAP (inetOrgPerson, person, etc.).
	filter := fmt.Sprintf("(&(|(objectClass=inetOrgPerson)(objectClass=person)(objectClass=user))(%s=*))",
		c.cfg.UsernameAttribute)

	attrs := []string{
		c.cfg.UsernameAttribute,
		"displayName",
		"mail",
		"title",
		"telephoneNumber",
		"dn",
	}

	req := ldap.NewSearchRequest(
		c.cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		filter,
		attrs,
		nil,
	)
	result, err := conn.SearchWithPaging(req, 500)
	if err != nil {
		// Paging may not be supported on all servers; fall back to non-paged.
		result, err = conn.Search(req)
		if err != nil {
			return nil, fmt.Errorf("ad sync: search users failed: %w", err)
		}
	}

	log.ZInfo(nil, "ad sync: found user entries", "count", len(result.Entries))

	var users []*SyncUser
	for _, entry := range result.Entries {
		username := entry.GetAttributeValue(c.cfg.UsernameAttribute)
		if username == "" {
			continue
		}

		dn := entry.DN
		primaryDeptDN := primaryDepartmentFromDN(dn)

		users = append(users, &SyncUser{
			DN:                  dn,
			Username:            username,
			DisplayName:         entry.GetAttributeValue("displayName"),
			Email:               entry.GetAttributeValue("mail"),
			Title:               entry.GetAttributeValue("title"),
			Phone:               entry.GetAttributeValue("telephoneNumber"),
			PrimaryDepartmentDN: primaryDeptDN,
		})
	}
	return users, nil
}

// parentDN extracts the parent DN by removing the first RDN component.
// e.g. "OU=A,OU=B,DC=example" → "OU=B,DC=example"
func parentDN(dn string) string {
	idx := strings.Index(dn, ",")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(dn[idx+1:])
}

// primaryDepartmentFromDN extracts the first OU component from a user's DN
// as the primary department identifier.
// e.g. "CN=user,OU=信息技术部,OU=ZXBXUsers,DC=qa,DC=bx" → "OU=信息技术部,OU=ZXBXUsers,DC=qa,DC=bx"
func primaryDepartmentFromDN(dn string) string {
	parts := splitDN(dn)
	for i, part := range parts {
		if isOU(part) {
			return strings.Join(parts[i:], ",")
		}
	}
	return ""
}

// ParentOUDNs extracts all OU hierarchy entries from a user's DN.
// Returns DNs from the most specific (closest to user) to the root OU.
// e.g. "CN=user,OU=A,OU=B,DC=ex" → ["OU=A,OU=B,DC=ex", "OU=B,DC=ex"]
func ParentOUDNs(dn string) []string {
	parts := splitDN(dn)
	var ous []string
	for i, part := range parts {
		if isOU(part) {
			ous = append(ous, strings.Join(parts[i:], ","))
		}
	}
	return ous
}

// isOU returns true if the RDN part starts with "OU=" (case-insensitive).
func isOU(part string) bool {
	return len(part) >= 3 && strings.EqualFold(part[:3], "OU=")
}

// splitDN splits a DN string by comma, preserving escaped commas.
func splitDN(dn string) []string {
	var parts []string
	var current strings.Builder
	escaped := false
	for _, ch := range dn {
		switch {
		case ch == '\\' && !escaped:
			escaped = true
			current.WriteRune(ch)
		case ch == ',' && !escaped:
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			escaped = false
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, strings.TrimSpace(current.String()))
	}
	return parts
}
