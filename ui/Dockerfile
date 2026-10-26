# Build stage: compile the Vue app
FROM registry.access.redhat.com/ubi9/nodejs-20:latest AS build
USER 0
WORKDIR /src
COPY package.json ./
RUN npm install
COPY index.html vite.config.js ./
COPY src ./src
RUN npm run build

# Runtime stage: Red Hat nginx image, serving on 8080 as non-root
FROM registry.access.redhat.com/ubi9/nginx-122:latest
USER 0
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build /src/dist /opt/app-root/src
RUN chmod -R g+rwX /opt/app-root/src
USER 1001
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
