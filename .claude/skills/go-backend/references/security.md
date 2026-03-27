# Security Practices Reference

Go security guidelines based on OWASP Go Secure Coding Practices. Read this when implementing authentication, handling user input, or working with sensitive data.

**Reference:** [OWASP Go-SCP](https://github.com/OWASP/Go-SCP)

## Input Validation

User input must be considered insecure by default. Validate on trusted systems (server-side), never rely on client-side validation alone.

### Validation Libraries

```go
import "github.com/go-playground/validator/v10"

type CreateUserInput struct {
    Username string `validate:"required,alphanum,min=3,max=32"`
    Email    string `validate:"required,email"`
    Age      int    `validate:"gte=18,lte=120"`
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (*User, error) {
    validate := validator.New()
    if err := validate.Struct(input); err != nil {
        return nil, fmt.Errorf("invalid input: %w", err)
    }
    // proceed with validated input
}
```

### Key Principles

- Validate all input: query params, headers, JSON bodies, file uploads
- Use allowlists over blocklists where possible
- Validate data types, length, format, and range
- Reject invalid input; don't try to sanitize and use it

## SQL Injection Prevention

**Always use prepared statements.** Never concatenate user input into SQL queries.

```go
// WRONG - vulnerable to SQL injection
query := "SELECT * FROM users WHERE id = " + userID
rows, _ := db.Query(query)

// CORRECT - parameterized query
query := "SELECT * FROM users WHERE id = $1"
rows, _ := db.QueryContext(ctx, query, userID)
```

Placeholder syntax varies by database:
- PostgreSQL: `$1`, `$2`, `$3`
- MySQL: `?`
- Oracle: `:param1`

For dynamic table/column names (avoid if possible), use allowlists:

```go
var allowedSortColumns = map[string]bool{
    "created_at": true,
    "name":       true,
    "email":      true,
}

func buildQuery(sortBy string) (string, error) {
    if !allowedSortColumns[sortBy] {
        return "", fmt.Errorf("invalid sort column: %s", sortBy)
    }
    return fmt.Sprintf("SELECT * FROM users ORDER BY %s", sortBy), nil
}
```

## XSS Prevention

Use `html/template` for HTML output, never `text/template`:

```go
import "html/template"

// html/template auto-escapes output in HTML context
tmpl := template.Must(template.New("page").Parse(`
    <h1>Hello, {{.Name}}</h1>
`))
```

For JSON APIs, set proper content-type headers:

```go
w.Header().Set("Content-Type", "application/json")
```

## Password Handling

**Use bcrypt.** Don't roll your own password hashing.

```go
import "golang.org/x/crypto/bcrypt"

// Hashing a password (at registration)
func HashPassword(password string) (string, error) {
    hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return "", fmt.Errorf("hashing password: %w", err)
    }
    return string(hash), nil
}

// Verifying a password (at login)
func VerifyPassword(hash, password string) error {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
```

**Never:**
- Store passwords in plaintext
- Use MD5 or SHA-1/SHA-256 alone (without proper KDF)
- Log passwords or include them in error messages

## Cryptography

### Random Number Generation

**Always use `crypto/rand` for security-sensitive randomness:**

```go
import "crypto/rand"

// Generate secure random bytes
token := make([]byte, 32)
if _, err := rand.Read(token); err != nil {
    return fmt.Errorf("generating token: %w", err)
}

// Generate secure random string
func SecureRandomString(length int) (string, error) {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    result := make([]byte, length)
    for i := range result {
        num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
        if err != nil {
            return "", err
        }
        result[i] = charset[num.Int64()]
    }
    return string(result), nil
}
```

**Never use `math/rand` for:**
- Session tokens
- API keys
- Encryption keys
- Password reset tokens
- Any security-sensitive value

### Encryption

Use authenticated encryption (GCM mode) for data encryption:

```go
import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
)

func Encrypt(key, plaintext []byte) ([]byte, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }
    
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }
    
    nonce := make([]byte, gcm.NonceSize())
    if _, err := rand.Read(nonce); err != nil {
        return nil, err
    }
    
    return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
```

For modern crypto needs, consider `golang.org/x/crypto/nacl/secretbox`.

## Session Management

```go
import "github.com/gorilla/sessions"

var store = sessions.NewCookieStore([]byte(os.Getenv("SESSION_KEY")))

func init() {
    store.Options = &sessions.Options{
        Path:     "/",
        MaxAge:   3600,           // 1 hour
        HttpOnly: true,           // Prevent XSS access
        Secure:   true,           // HTTPS only
        SameSite: http.SameSiteStrictMode,
    }
}
```

**Key practices:**
- Generate new session ID on authentication
- Set appropriate expiration
- Use HttpOnly and Secure flags
- Implement session invalidation on logout

## Secrets Management

**Never hardcode secrets.** Use environment variables or a secrets manager:

```go
// Good: from environment
dbPassword := os.Getenv("DB_PASSWORD")

// Better: from secrets manager (Vault, AWS Secrets Manager, etc.)
secret, err := secretsClient.GetSecret(ctx, "db-password")
```

**Never:**
- Commit secrets to git
- Log secrets
- Include secrets in error messages
- Pass secrets as command-line arguments (visible in process list)

## HTTP Security Headers

Set security headers in middleware:

```go
func SecurityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "1; mode=block")
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")
        next.ServeHTTP(w, r)
    })
}
```

## Rate Limiting

Protect endpoints from abuse:

```go
import "golang.org/x/time/rate"

type RateLimiter struct {
    limiters map[string]*rate.Limiter
    mu       sync.RWMutex
}

func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    limiter, exists := rl.limiters[ip]
    if !exists {
        limiter = rate.NewLimiter(rate.Every(time.Second), 10) // 10 req/sec
        rl.limiters[ip] = limiter
    }
    return limiter
}
```

## Error Handling Security

Don't leak internal details in error responses:

```go
// WRONG - leaks internal info
http.Error(w, err.Error(), http.StatusInternalServerError)

// CORRECT - generic message to client, detailed log internally
logger.Error("database query failed", slog.Any("error", err))
http.Error(w, "An internal error occurred", http.StatusInternalServerError)
```

## File Upload Security

```go
func handleUpload(w http.ResponseWriter, r *http.Request) {
    // Limit upload size
    r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
    
    file, header, err := r.FormFile("file")
    if err != nil {
        http.Error(w, "Invalid file", http.StatusBadRequest)
        return
    }
    defer file.Close()
    
    // Validate file type by content, not extension
    buffer := make([]byte, 512)
    if _, err := file.Read(buffer); err != nil {
        http.Error(w, "Cannot read file", http.StatusBadRequest)
        return
    }
    contentType := http.DetectContentType(buffer)
    
    allowedTypes := map[string]bool{
        "image/jpeg": true,
        "image/png":  true,
        "application/pdf": true,
    }
    if !allowedTypes[contentType] {
        http.Error(w, "File type not allowed", http.StatusBadRequest)
        return
    }
    
    // Generate safe filename, don't use user-provided name
    safeFilename := fmt.Sprintf("%s%s", uuid.New().String(), filepath.Ext(header.Filename))
    
    // Store outside web root
    // ...
}
```
