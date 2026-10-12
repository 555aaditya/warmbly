package clay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/utils"
)

// row is one Clay row read into contact fields.
type row struct {
	email        string
	first        string
	last         string
	company      string
	phone        string
	subscribed   string
	verification string
	categories   []string
	custom       map[string]string
	campaign     *uuid.UUID
	campaignErr  bool
}

// Clay column names vary by table; each field accepts the spellings people use.
var (
	emailKeys    = []string{"email", "email_address", "work_email", "business_email", "e_mail", "contact_email", "person_email", "emailaddress", "workemail", "mail"}
	firstKeys    = []string{"first_name", "firstname", "first", "given_name"}
	lastKeys     = []string{"last_name", "lastname", "last", "surname", "family_name"}
	fullKeys     = []string{"full_name", "fullname", "name", "person_name", "contact_name"}
	companyKeys  = []string{"company", "company_name", "companyname", "organization", "organization_name", "account_name", "employer"}
	phoneKeys    = []string{"phone", "phone_number", "phonenumber", "mobile", "mobile_phone", "mobile_number", "work_phone", "direct_phone"}
	subKeys      = []string{"subscribed"}
	tagKeys      = []string{"tags", "tag", "labels", "label", "categories", "category"}
	campaignKeys = []string{"campaign_id", "warmbly_campaign_id"}
	verifyKeys   = []string{"verification_status", "email_status", "email_verification", "email_validation_status"}
	// customAliases fold common enrichment columns onto one custom field each.
	customAliases = map[string]string{
		"job_title": "title", "title": "title", "position": "title", "role": "title",
		"linkedin": "linkedin_url", "linkedin_url": "linkedin_url", "linkedin_profile": "linkedin_url",
		"linkedin_profile_url": "linkedin_url", "person_linkedin_url": "linkedin_url",
		"website": "website", "company_website": "website", "website_url": "website",
		"domain": "company_domain", "company_domain": "company_domain",
		"location": "location", "city": "city", "state": "state", "country": "country",
		"industry": "industry", "company_industry": "industry",
		"employee_count": "employee_count", "company_size": "employee_count", "headcount": "employee_count",
	}
	reNonKey = regexp.MustCompile(`[^a-z0-9]+`)
	reBadKey = regexp.MustCompile(`[^A-Za-z0-9_ -]+`)
)

// keyOf folds a column name for matching: lower case, words joined by underscores.
func keyOf(k string) string {
	return strings.Trim(reNonKey.ReplaceAllString(strings.ToLower(strings.TrimSpace(k)), "_"), "_")
}

// parseBody reads a single row object, an array of them, or an object
// carrying them under rows, contacts or records.
func parseBody(body []byte) ([]map[string]any, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, errx.New(errx.BadRequest, "send a JSON object with at least an email")
	}
	var rows []map[string]any
	switch body[0] {
	case '[':
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, errx.New(errx.BadRequest, "the body must be a JSON object or an array of objects")
		}
	case '{':
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			return nil, errx.New(errx.BadRequest, "the body is not valid JSON")
		}
		rows = []map[string]any{obj}
		for _, k := range []string{"rows", "contacts", "records"} {
			list, ok := obj[k].([]any)
			if !ok {
				continue
			}
			rows = rows[:0]
			for _, item := range list {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, errx.New(errx.BadRequest, k+" must be an array of objects")
				}
				rows = append(rows, m)
			}
			break
		}
	default:
		return nil, errx.New(errx.BadRequest, "the body must be a JSON object or an array of objects")
	}
	if len(rows) == 0 {
		return nil, errx.New(errx.BadRequest, "send at least one row")
	}
	if len(rows) > MaxRows {
		return nil, errx.New(errx.BadRequest, fmt.Sprintf("at most %d rows per request", MaxRows))
	}
	return rows, nil
}

// normalizeRow reads one Clay row. Anything that is not a known field becomes
// a custom field, so every enrichment column travels.
func normalizeRow(m map[string]any) row {
	r := row{custom: map[string]string{}}
	folded := make(map[string]any, len(m))
	original := make(map[string]string, len(m))
	for k, v := range m {
		fk := keyOf(k)
		if fk == "" || strings.HasPrefix(k, "_") {
			continue
		}
		if _, dup := folded[fk]; dup && scalar(v) == "" {
			continue
		}
		folded[fk] = v
		original[fk] = k
	}
	used := map[string]bool{}
	pick := func(keys []string) string {
		for _, k := range keys {
			if v, ok := folded[k]; ok {
				if s := scalar(v); s != "" {
					used[k] = true
					return s
				}
				used[k] = true
			}
		}
		return ""
	}
	r.email = pick(emailKeys)
	r.first = pick(firstKeys)
	r.last = pick(lastKeys)
	if full := pick(fullKeys); full != "" && r.first == "" && r.last == "" {
		r.first, r.last = splitName(full)
	}
	r.company = pick(companyKeys)
	r.phone = pick(phoneKeys)
	r.subscribed = pick(subKeys)
	r.verification = pick(verifyKeys)
	for _, k := range tagKeys {
		v, ok := folded[k]
		if !ok {
			continue
		}
		used[k] = true
		switch t := v.(type) {
		case []any:
			for _, item := range t {
				if s := scalar(item); s != "" {
					r.categories = append(r.categories, truncate(s, 100))
				}
			}
		default:
			for _, s := range strings.FieldsFunc(scalar(v), func(c rune) bool { return c == ',' || c == ';' }) {
				if s = strings.TrimSpace(s); s != "" {
					r.categories = append(r.categories, truncate(s, 100))
				}
			}
		}
	}
	if len(r.categories) > 20 {
		r.categories = r.categories[:20]
	}
	if c := pick(campaignKeys); c != "" {
		if id, err := uuid.Parse(c); err == nil {
			r.campaign = &id
		} else {
			r.campaignErr = true
		}
	}
	if cf, ok := folded["custom_fields"].(map[string]any); ok {
		used["custom_fields"] = true
		for k, v := range cf {
			r.setCustom(k, v)
		}
	}
	for fk, v := range folded {
		if used[fk] {
			continue
		}
		if alias, ok := customAliases[fk]; ok {
			if s := scalar(v); s != "" {
				r.custom[alias] = truncate(s, 2000)
			}
			continue
		}
		r.setCustom(original[fk], v)
	}
	return r
}

// setCustom stores a value under a key the template engine can address.
func (r *row) setCustom(k string, v any) {
	s := scalar(v)
	if s == "" {
		return
	}
	key := utils.NormalizeJSONKey(k)
	if !utils.IsValidJSONKey(key) {
		key = utils.NormalizeJSONKey(strings.Trim(reBadKey.ReplaceAllString(key, "_"), "_ -"))
		if !utils.IsValidJSONKey(key) {
			return
		}
	}
	if len(key) > 64 {
		return
	}
	r.custom[key] = truncate(s, 2000)
}

// scalar renders a cell value as text; objects are not cell values.
func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s := scalar(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func splitName(full string) (string, string) {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Host
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
