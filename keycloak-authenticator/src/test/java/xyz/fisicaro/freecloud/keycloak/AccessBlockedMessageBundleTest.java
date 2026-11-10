package xyz.fisicaro.freecloud.keycloak;

import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashSet;
import java.util.Properties;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

class AccessBlockedMessageBundleTest {

    private static final String TEMPLATE_PATH =
        "/theme-resources/templates/access-blocked.ftl";
    private static final String BUNDLE_PATH =
        "/theme-resources/messages/messages_en.properties";
    private static final Pattern MSG_KEY =
        Pattern.compile("msg\\(\\s*[\"']([^\"']+)[\"']");

    @Test
    void everyMsgKeyInTemplateResolvesInEnglishBundle() throws IOException {
        String template;
        try (InputStream in = getClass().getResourceAsStream(TEMPLATE_PATH)) {
            assertNotNull(in, "template not found on classpath: " + TEMPLATE_PATH);
            template = new String(in.readAllBytes(), StandardCharsets.UTF_8);
        }

        Set<String> keys = new LinkedHashSet<>();
        Matcher matcher = MSG_KEY.matcher(template);
        while (matcher.find()) {
            keys.add(matcher.group(1));
        }
        assertFalse(keys.isEmpty(), "no msg(\"...\") keys found in " + TEMPLATE_PATH);

        Properties bundle = new Properties();
        try (InputStream in = getClass().getResourceAsStream(BUNDLE_PATH)) {
            if (in == null) {
                fail("message bundle not found on classpath: " + BUNDLE_PATH
                    + " (template keys would render unresolved, e.g. ???accessBlocked???)");
            }
            bundle.load(in);
        }

        for (String key : keys) {
            String value = bundle.getProperty(key);
            assertNotNull(value, "missing key in " + BUNDLE_PATH + ": " + key);
            assertFalse(value.isBlank(), "blank value for key: " + key);
            assertTrue(!value.contains("???"),
                "value looks like an unresolved placeholder for key: " + key);
        }
    }
}
