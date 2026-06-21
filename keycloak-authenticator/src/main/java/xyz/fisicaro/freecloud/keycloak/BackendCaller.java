package xyz.fisicaro.freecloud.keycloak;

@FunctionalInterface
interface BackendCaller {
    BackendResponse call(String url, String token, String body) throws Exception;
}
