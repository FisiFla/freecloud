package handlers

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/provisioning"
)

// ReloadProvisioningConnectors rebuilds the outbound provisioning connector
// registry from app_provisioning_config.
func ReloadProvisioningConnectors(ctx context.Context, engine *provisioning.Engine, db DBPool, logger *zap.Logger) error {
	if engine == nil || db == nil {
		return nil
	}
	connectors, err := LoadProvisioningConnectors(ctx, db, logger)
	if err != nil {
		return err
	}
	engine.ReplaceConnectors(connectors)
	return nil
}

// LoadProvisioningConnectors returns enabled, fully configured connectors keyed
// by app UUID.
func LoadProvisioningConnectors(ctx context.Context, db DBPool, logger *zap.Logger) (map[string]provisioning.Connector, error) {
	rows, err := db.Query(ctx,
		`SELECT app_id::TEXT, connector_type, COALESCE(endpoint_url, ''), bearer_token_enc
		 FROM app_provisioning_config
		 WHERE enabled = true`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	connectors := map[string]provisioning.Connector{}
	for rows.Next() {
		var appID, connectorType, endpoint string
		var tokenEnc *string
		if err := rows.Scan(&appID, &connectorType, &endpoint, &tokenEnc); err != nil {
			logger.Warn("provisioning connectors: scan failed", zap.Error(err))
			continue
		}

		token := ""
		if tokenEnc != nil && *tokenEnc != "" {
			plaintext, err := decryptProvisioningToken(*tokenEnc)
			if err != nil {
				logger.Warn("provisioning connectors: decrypt token failed",
					zap.String("app_id", appID), zap.Error(err))
				continue
			}
			token = plaintext
		}

		connector, ok := buildProvisioningConnector(connectorType, endpoint, token)
		if !ok {
			logger.Warn("provisioning connectors: enabled config is incomplete",
				zap.String("app_id", appID),
				zap.String("connector_type", connectorType))
			continue
		}
		connectors[appID] = connector
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return connectors, nil
}

func buildProvisioningConnector(connectorType, endpoint, token string) (provisioning.Connector, bool) {
	connectorType = strings.TrimSpace(connectorType)
	endpoint = strings.TrimSpace(endpoint)
	token = strings.TrimSpace(token)

	switch connectorType {
	case "scim":
		if endpoint == "" || token == "" {
			return nil, false
		}
		return provisioning.NewSCIMConnector(endpoint, token), true
	case "slack":
		if token == "" {
			return nil, false
		}
		return provisioning.NewSlackConnector(token), true
	case "github":
		if endpoint == "" || token == "" {
			return nil, false
		}
		return provisioning.NewGitHubConnector(endpoint, token), true
	default:
		return nil, false
	}
}
