# syntax=docker/dockerfile:1.7

# Chetter Agent — Java Spring Boot Variant (Reference)
# =========================================================================
# Java Spring Boot development environment with JDK 21, Maven, Gradle,
# Liquibase for migrations, and PostgreSQL client tools.
#
# Teams should copy this Dockerfile to their chetter-config repo and
# customize as needed. Build from the chetter repo root:
#
#   docker build -f runner/images/examples/java-spring/Dockerfile \
#     -t ghcr.io/flatout-works/chetter-agent:java-spring .
#
# Or from chetter-config:
#   docker build -t my-org/chetter-agent:java-spring .
# =========================================================================

ARG BASE_IMAGE=ghcr.io/flatout-works/chetter-agent-base:main

FROM ${BASE_IMAGE}

# JDK 21 LTS (Eclipse Temurin)
ARG JDK_VERSION=21
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
        wget \
        gnupg \
    && mkdir -p /etc/apt/keyrings \
    && wget -qO - https://packages.adoptium.net/artifactory/api/gpg/key/public \
       | gpg --dearmor -o /etc/apt/keyrings/adoptium.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb bookworm main" \
       > /etc/apt/sources.list.d/adoptium.list \
    && apt-get update && apt-get install -y --no-install-recommends \
        temurin-${JDK_VERSION}-jdk \
    && rm -rf /var/lib/apt/lists/*

ENV JAVA_HOME=/usr/lib/jvm/temurin-${JDK_VERSION}-jdk-amd64
ENV PATH="${JAVA_HOME}/bin:${PATH}"

# Maven
ARG MAVEN_VERSION=3.9.9
RUN curl -fsSL "https://dlcdn.apache.org/maven/maven-3/${MAVEN_VERSION}/binaries/apache-maven-${MAVEN_VERSION}-bin.tar.gz" \
    | tar -C /usr/local -xzf - \
    && ln -sf "/usr/local/apache-maven-${MAVEN_VERSION}/bin/mvn" /usr/local/bin/mvn

# Gradle
ARG GRADLE_VERSION=8.12
RUN curl -fsSL "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip" -o /tmp/gradle.zip \
    && unzip -q /tmp/gradle.zip -d /usr/local \
    && rm /tmp/gradle.zip \
    && ln -sf "/usr/local/gradle-${GRADLE_VERSION}/bin/gradle" /usr/local/bin/gradle

# Liquibase CLI
ARG LIQUIBASE_VERSION=4.31.0
RUN curl -fsSL "https://github.com/liquibase/liquibase/releases/download/v${LIQUIBASE_VERSION}/liquibase-${LIQUIBASE_VERSION}.tar.gz" \
    -o /tmp/liquibase.tar.gz \
    && mkdir -p /usr/local/liquibase \
    && tar -C /usr/local/liquibase -xzf /tmp/liquibase.tar.gz \
    && rm /tmp/liquibase.tar.gz \
    && ln -sf /usr/local/liquibase/liquibase /usr/local/bin/liquibase

# PostgreSQL client tools (psql, pg_dump, pg_restore, etc.)
ARG POSTGRES_VERSION=17
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
        postgresql-client \
    && rm -rf /var/lib/apt/lists/*

# Verify installations
RUN java -version \
    && mvn -version \
    && gradle -version \
    && liquibase --version \
    && psql --version

WORKDIR /workspace
