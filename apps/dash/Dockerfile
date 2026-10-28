# syntax=docker/dockerfile:1
# The dashboard. Build from the repo root, naming the API it will call:
#   docker build -f apps/dash/Dockerfile \
#     --build-arg NEXT_PUBLIC_API_URL=https://api.example.com \
#     --build-arg NEXT_PUBLIC_APP_URL=https://app.example.com -t airship-dash .
# NEXT_PUBLIC_API_URL is inlined into the JavaScript at build time, so each
# deployment with a different API URL needs its own image.

FROM node:24-alpine AS build
RUN npm install --global pnpm@11.22.0
WORKDIR /repo
COPY . .
RUN --mount=type=cache,id=pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
ARG NEXT_PUBLIC_API_URL
ARG NEXT_PUBLIC_APP_URL
RUN test -n "$NEXT_PUBLIC_API_URL" || { echo "set --build-arg NEXT_PUBLIC_API_URL"; exit 1; }
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL NEXT_PUBLIC_APP_URL=$NEXT_PUBLIC_APP_URL NEXT_TELEMETRY_DISABLED=1
RUN NODE_ENV=production pnpm --filter @airship/dash build

FROM node:24-alpine AS dash
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 HOSTNAME=0.0.0.0 PORT=3000
COPY --from=build --chown=node:node /repo/apps/dash/.next/standalone ./
COPY --from=build --chown=node:node /repo/apps/dash/.next/static ./apps/dash/.next/static
COPY --from=build --chown=node:node /repo/apps/dash/public ./apps/dash/public
USER node
EXPOSE 3000
CMD ["node", "apps/dash/server.js"]
