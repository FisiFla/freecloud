<#import "template.ftl" as layout>
<@layout.registrationLayout displayInfo=false; section>
    <#if section = "title">
        ${msg("accessBlocked")}
    <#elseif section = "header">
        ${msg("accessBlocked")}
    <#elseif section = "form">
        <div class="alert alert-error">
            <p>${msg("accessBlockedMessage")}</p>
            <#if reasons?? && reasons?has_content>
            <p>${msg("accessBlockedReasons", reasons)}</p>
            </#if>
        </div>
    </#if>
</@layout.registrationLayout>
