# syntax=docker/dockerfile:1.7
ARG NODE_VERSION=20.12.2
ARG ALPINE=3.19

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine${ALPINE} AS base
WORKDIR /app

FROM base AS deps
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

FROM base AS build
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM gcr.io/distroless/nodejs20-debian12:nonroot AS runtime
COPY --from=build /app/dist /app/dist
COPY --from=busybox:1.36.1 /bin/busybox /bin/busybox
COPY --from=ghcr.io/astral-sh/uv:0.4.9 /uv /usr/local/bin/uv
CMD ["/app/dist/server.js"]

FROM rust:1.78-bookworm AS builder
FROM python:3.12-slim-bookworm
FROM busybox:1.36.1@sha256:0000000000000000000000000000000000000000000000000000000000000000
FROM mcr.microsoft.com/dotnet/aspnet:8.0
FROM localhost:5000/team/img:2.0.1
FROM scratch
FROM ubuntu:22.04 as legacy-lowercase-as
