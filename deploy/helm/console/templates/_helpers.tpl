{{/* SPDX-FileCopyrightText: Copyright (c) 2026 OpenShell Dashboard contributors
SPDX-License-Identifier: Apache-2.0 */}}

{{/*
Expand the name of the chart
*/}}
{{- define "console.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create default fully qualified app name
*/}}
{{- define "console.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label
*/}}
{{- define "console.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Labels
*/}}
{{- define "console.selectorLabels" -}}
app.kubernetes.io/name: console
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "console.labels" -}}
helm.sh/chart: {{ include "console.chart" . }}
{{ include "console.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Service account creation
*/}}
{{- define "console.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "console.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Image definition
*/}}
{{- define "console.image" -}}
{{- $image := .image -}}
{{- $global := .root.Values.global.image -}}
{{- $registry := $image.registry | default $global.registry -}}
{{- $repository := ternary (printf "%s/%s" $registry $image.repository) $image.repository (ne $registry "") -}}
{{- if $image.digest -}}
{{- printf "%s@%s" $repository $image.digest -}}
{{- else -}}
{{- printf "%s:%s" $repository (($image.tag | default $global.tag | default .root.Chart.AppVersion) | toString) -}}
{{- end -}}
{{- end -}}

{{/*
Sets the gateway URL 
*/}}
{{- define "console.gatewayURL" -}}
{{- .Values.gateway.url | default (printf "%s://%s.%s.svc.cluster.local:8080" (ternary "grpcs" "grpc" .Values.gateway.tls.enabled) .Values.gateway.serviceName .Release.Namespace) -}}
{{- end -}}

{{/*
Sets the dashboard OIDC redirect URL
*/}}
{{- define "console.redirectURL" -}}
{{- if .Values.oidc.redirectURL -}}
{{- .Values.oidc.redirectURL -}}
{{- else if .Values.openshiftRoute.enabled -}}
{{- printf "https://%s/oauth2/callback" .Values.openshiftRoute.host -}}
{{- else if .Values.httpRoute.enabled -}}
{{- printf "https://%s/oauth2/callback" (first .Values.httpRoute.hostnames) -}}
{{- end -}}
{{- end -}}

{{/*
The IdP logout URL oauth2-proxy calls on sign-out. "auto" derives Keycloak's from
the issuer and sets none for other providers; empty sets none.
*/}}
{{- define "console.backendLogoutURL" -}}
{{- $url := .Values.oidc.backendLogoutURL | default "" | toString -}}
{{- if eq $url "auto" -}}
{{- if and (eq .Values.oidc.provider "keycloak-oidc") .Values.oidc.issuer -}}
{{- printf "%s/protocol/openid-connect/logout?id_token_hint={id_token}" (trimSuffix "/" .Values.oidc.issuer) -}}
{{- end -}}
{{- else -}}
{{- $url -}}
{{- end -}}
{{- end -}}

{{/*
Value validation
*/}}
{{- define "console.validateValues" -}}
{{- $url := include "console.gatewayURL" . -}}
{{- if not (or (hasPrefix "grpcs://" $url) (hasPrefix "https://" $url) (hasPrefix "grpc://" $url) (hasPrefix "http://" $url)) -}}
{{- fail "gateway.url must start with grpcs://, https://, grpc://, or http://" -}}
{{- end -}}
{{- if and (or (hasPrefix "grpc://" $url) (hasPrefix "http://" $url)) (or .Values.gateway.caSecretName .Values.gateway.clientCertSecretName) -}}
{{- fail "plaintext gateway.url requires gateway.caSecretName and gateway.clientCertSecretName to be empty" -}}
{{- end -}}
{{- if and (or (hasPrefix "grpcs://" $url) (hasPrefix "https://" $url)) (not .Values.gateway.caSecretName) -}}
{{- fail "TLS gateway.url requires gateway.caSecretName" -}}
{{- end -}}
{{- if and .Values.authProxy.forceHTTPS (not .Values.authProxy.tls.secretName) -}}
{{- fail "authProxy.forceHTTPS requires authProxy.tls.secretName; use ingress TLS without forceHTTPS for edge termination" -}}
{{- end -}}
{{- if or (hasKey .Values.podLabels "app.kubernetes.io/name") (hasKey .Values.podLabels "app.kubernetes.io/instance") -}}
{{- fail "podLabels cannot override app.kubernetes.io/name or app.kubernetes.io/instance" -}}
{{- end -}}
{{- if and .Values.openshiftRoute.enabled .Values.httpRoute.enabled -}}
{{- fail "enable only one of openshiftRoute and httpRoute" -}}
{{- end -}}
{{- if and .Values.httpRoute.enabled .Values.networkPolicy.enabled (not .Values.networkPolicy.ingressNamespace) (not .Values.networkPolicy.ingressFrom) -}}
{{- fail "httpRoute with NetworkPolicy requires networkPolicy.ingressNamespace or networkPolicy.ingressFrom for Gateway data-plane ingress" -}}
{{- end -}}
{{- if and .Values.openshiftRoute.enabled (not (has .Values.openshiftRoute.termination (list "edge" "reencrypt"))) -}}
{{- fail "openshiftRoute.termination must be edge or reencrypt (not passthrough)" -}}
{{- end -}}
{{- if and (eq .Values.openshiftRoute.termination "reencrypt") (or (not .Values.authProxy.tls.secretName) (not .Values.openshiftRoute.destinationCACertificate)) -}}
{{- fail "reencrypt requires authProxy.tls.secretName and openshiftRoute.destinationCACertificate" -}}
{{- end -}}
{{- if and .Values.authProxy.tls.secretName (ne .Values.openshiftRoute.termination "reencrypt") -}}
{{- fail "authProxy.tls.secretName requires openshiftRoute.termination=reencrypt" -}}
{{- end -}}
{{- if and .Values.httpRoute.enabled (not .Values.httpRoute.hostnames) (not .Values.oidc.redirectURL) -}}
{{- fail "httpRoute.hostnames or oidc.redirectURL is required for OIDC callback" -}}
{{- end -}}
{{- if and .Values.openshiftRoute.enabled (not .Values.openshiftRoute.host) (not .Values.oidc.redirectURL) -}}
{{- fail "openshiftRoute.host or oidc.redirectURL is required for OIDC callback" -}}
{{- end -}}
{{- if and .Values.httpRoute.gateway.create (not .Values.httpRoute.enabled) -}}
{{- fail "httpRoute.gateway.create requires httpRoute.enabled" -}}
{{- end -}}
{{- if and .Values.httpRoute.gateway.create .Values.httpRoute.gateway.namespace (ne .Values.httpRoute.gateway.namespace .Release.Namespace) -}}
{{- fail "a created Gateway must be in the release namespace" -}}
{{- end -}}
{{- if and .Values.httpRoute.gateway.create (eq .Values.httpRoute.gateway.listener.protocol "HTTPS") (not .Values.httpRoute.gateway.listener.tls.certificateRefs) -}}
{{- fail "httpRoute.gateway.listener.tls.certificateRefs is required for an HTTPS Gateway" -}}
{{- end -}}
{{- if and .Values.httpRoute.gateway.create (not (has .Values.httpRoute.gateway.listener.protocol (list "HTTP" "HTTPS"))) -}}
{{- fail "httpRoute.gateway.listener.protocol must be HTTP or HTTPS" -}}
{{- end -}}
{{- if not .Values.oidc.existingSecret -}}
{{- fail "oidc.existingSecret must name a Secret with client-secret and cookie-secret" -}}
{{- end -}}
{{- if not .Values.oidc.issuer -}}
{{- fail "oidc.issuer must be set and match gateway server.oidc.issuer" -}}
{{- end -}}
{{- if and (not .Values.oidc.clientId) (not .Values.oidc.clientIdSecretKey) -}}
{{- fail "oidc.clientId or oidc.clientIdSecretKey is required" -}}
{{- end -}}
{{- if and .Values.oidc.allowedRoles (ne .Values.oidc.provider "keycloak-oidc") -}}
{{- fail "oidc.allowedRoles requires oidc.provider=keycloak-oidc" -}}
{{- end -}}
{{- if and .Values.gateway.clientCertSecretName (not (or .Values.oidc.allowedGroups .Values.oidc.allowedRoles)) -}}
{{- fail "gateway.clientCertSecretName requires oidc.allowedGroups or oidc.allowedRoles to restrict shared certificate access" -}}
{{- end -}}
{{- if and .Values.autoscaling.enabled (or (lt (int .Values.autoscaling.minReplicas) 1) (lt (int .Values.autoscaling.maxReplicas) (int .Values.autoscaling.minReplicas))) -}}
{{- fail "autoscaling requires minReplicas >= 1 and maxReplicas >= minReplicas" -}}
{{- end -}}
{{- if and .Values.autoscaling.enabled (not (or .Values.autoscaling.targetCPUUtilizationPercentage .Values.autoscaling.targetMemoryUtilizationPercentage)) -}}
{{- fail "autoscaling requires at least one utilization target" -}}
{{- end -}}
{{- if .Values.autoscaling.enabled -}}
{{- range $component := list (dict "name" "resources" "value" .Values.resources) (dict "name" "authProxy.resources" "value" .Values.authProxy.resources) -}}
{{- if and $.Values.autoscaling.targetCPUUtilizationPercentage (not (or (dig "requests" "cpu" "" $component.value) (dig "limits" "cpu" "" $component.value))) -}}
{{- fail (printf "autoscaling CPU target requires %s.requests.cpu or %s.limits.cpu" $component.name $component.name) -}}
{{- end -}}
{{- if and $.Values.autoscaling.targetMemoryUtilizationPercentage (not (or (dig "requests" "memory" "" $component.value) (dig "limits" "memory" "" $component.value))) -}}
{{- fail (printf "autoscaling memory target requires %s.requests.memory or %s.limits.memory" $component.name $component.name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
