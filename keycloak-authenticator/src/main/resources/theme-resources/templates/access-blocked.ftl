<#import "template.ftl" as layout>
<@layout.registrationLayout displayInfo=false; section>
    <#if section = "title">
        ${msg("accessBlocked")}
    <#elseif section = "header">
        ${msg("accessBlocked")}
    <#elseif section = "form">
        <div class="alert alert-error">
            <p>Access denied: your device does not meet posture requirements.</p>
            <#if reasons?? && reasons?has_content>
            <p>Reasons: ${reasons}</p>
            </#if>
        </div>
    </#if>
</@layout.registrationLayout>
