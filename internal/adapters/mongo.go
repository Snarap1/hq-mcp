package adapters

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"hq-mcp/internal/config"
)

// mongoTimeout bounds server selection so an unreachable host fails fast.
const mongoTimeout = 10 * time.Second

// Mongo is a mongo-driver client plus the profile's default database.
type Mongo struct {
	client   *mongo.Client
	database string
}

func (c *Mongo) Kind() string { return "mongodb" }
func (c *Mongo) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), mongoTimeout)
	defer cancel()
	_ = c.client.Disconnect(ctx)
}

// Client exposes the underlying client; it is only valid for mongodb profiles.
func (c *Mongo) Client() *mongo.Client { return c.client }

// Database is the profile's default database, or "" when none is configured.
func (c *Mongo) Database() string { return c.database }

// mongoURI builds the connection URI and default database. A mongodb:// or
// mongodb+srv:// conn_str is used as-is (its path names the default
// database); otherwise host and port build a single-host URI.
func mongoURI(p map[string]any) (string, string, error) {
	cs, err := config.OptConnStr(p)
	if err != nil {
		return "", "", err
	}
	database, err := config.OptStr(p, "database")
	if err != nil {
		return "", "", err
	}
	if cs != "" {
		if !strings.HasPrefix(cs, "mongodb://") && !strings.HasPrefix(cs, "mongodb+srv://") {
			return "", "", fmt.Errorf("mongodb conn_str must start with mongodb:// or mongodb+srv://")
		}
		if database == "" {
			// net/url accepts the comma-separated host list of a replica-set URI.
			if u, err := url.Parse(cs); err == nil {
				database = strings.TrimPrefix(u.Path, "/")
			}
		}
		return cs, database, nil
	}
	host, port, err := hostPort(p, "localhost", 27017)
	if err != nil {
		return "", "", err
	}
	return "mongodb://" + net.JoinHostPort(host, strconv.Itoa(port)), database, nil
}

func openMongo(p map[string]any) (DB, error) {
	uri, database, err := mongoURI(p)
	if err != nil {
		return nil, err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return nil, err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return nil, err
	}
	authSource, err := config.OptStr(p, "auth_source")
	if err != nil {
		return nil, err
	}
	secure, err := config.OptBool(p, "secure")
	if err != nil {
		return nil, err
	}
	opts := options.Client().ApplyURI(uri).
		SetServerSelectionTimeout(mongoTimeout).
		SetAppName("hq-mcp")
	// Explicit keys take precedence over the URI's credentials.
	if user != "" {
		cred := options.Credential{Username: user, Password: pass, AuthSource: authSource}
		if opts.Auth != nil {
			cred.AuthMechanism = opts.Auth.AuthMechanism
			if cred.AuthSource == "" {
				cred.AuthSource = opts.Auth.AuthSource
			}
		}
		opts.SetAuth(cred)
	} else if authSource != "" && opts.Auth != nil {
		opts.Auth.AuthSource = authSource
	}
	if secure {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	return &Mongo{client: client, database: database}, nil
}
