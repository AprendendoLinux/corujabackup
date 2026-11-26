# Site estático do Coruja Backup (página inicial, privacidade e termos), usado na tela de consentimento OAuth.
FROM nginxinc/nginx-unprivileged:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY index.html privacidade.html termos.html logo-120.png logo-512.png logo.svg /usr/share/nginx/html/
EXPOSE 8080
