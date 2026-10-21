package xyz.fisicaro.freecloud.keycloak;

import org.keycloak.Config;
import org.keycloak.authentication.Authenticator;
import org.keycloak.authentication.AuthenticatorFactory;
import org.keycloak.models.AuthenticationExecutionModel;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.KeycloakSessionFactory;
import org.keycloak.provider.ProviderConfigProperty;

import java.util.List;
import java.util.logging.Logger;

public class PostureCheckAuthenticatorFactory implements AuthenticatorFactory {

    // The Keycloak SPI does not put a logging facade on this module's
    // compile classpath, so the JDK logger is used: the message still reaches
    // the container's stderr, which is what `docker compose logs keycloak`
    // shows.
    private static final Logger LOG = Logger.getLogger(PostureCheckAuthenticatorFactory.class.getName());

    private static final AuthenticationExecutionModel.Requirement[] REQUIREMENT_CHOICES = {
        AuthenticationExecutionModel.Requirement.REQUIRED,
        AuthenticationExecutionModel.Requirement.DISABLED
    };

    @Override
    public String getId() {
        return "freecloud-posture-check";
    }

    @Override
    public Authenticator create(KeycloakSession session) {
        return new PostureCheckAuthenticator();
    }

    @Override
    public String getDisplayType() {
        return "FreeCloud Posture Check";
    }

    @Override
    public String getReferenceCategory() {
        return "posture";
    }

    @Override
    public boolean isConfigurable() {
        return false;
    }

    @Override
    public AuthenticationExecutionModel.Requirement[] getRequirementChoices() {
        return REQUIREMENT_CHOICES;
    }

    @Override
    public boolean isUserSetupAllowed() {
        return false;
    }

    @Override
    public String getHelpText() {
        return "Calls FreeCloud access/evaluate to gate login on device posture.";
    }

    @Override
    public List<ProviderConfigProperty> getConfigProperties() {
        return List.of();
    }

    @Override
    public void init(Config.Scope scope) {
        // The posture check is a FEATURE FLAG that fails OPEN: when it is
        // disabled the authenticator returns success without evaluating the
        // device at all. A production stack that sets the variable wrongly
        // would therefore run with no device, network, geo or time-window
        // enforcement and show no other symptom. Warn loudly, once, at
        // startup — but not in development, where turning it off is the point
        // (issue #202).
        boolean enabled = "true".equalsIgnoreCase(System.getenv("POSTURE_CHECK_ENABLED"));
        String appEnv = System.getenv("APP_ENV");
        boolean dev = "development".equals(appEnv) || "test".equals(appEnv);
        if (!enabled && !dev) {
            LOG.warning("SECURITY: device posture enforcement is DISABLED (POSTURE_CHECK_ENABLED is not \"true\","
                    + " APP_ENV=" + appEnv + "). Logins are NOT gated on device, network, geo or time-window"
                    + " policy. Set POSTURE_CHECK_ENABLED=true unless this is a development stack.");
        }
    }

    @Override
    public void postInit(KeycloakSessionFactory factory) {
        // No post-initialization needed
    }

    @Override
    public void close() {
        // Nothing to close
    }
}
