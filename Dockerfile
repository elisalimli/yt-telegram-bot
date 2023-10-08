FROM golang:1.21 as compiler
WORKDIR /src/app
COPY go.mod go.sum ./
RUN go mod download 
COPY . .

RUN go build -o ./a.out .

# FROM tnk4on/yt-dlp
FROM debian
RUN apt-get -qqy update && apt-get -qqy install curl ffmpeg python-is-python3

RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp && \
    chmod a+rx /usr/local/bin/yt-dlp  # Make executable

COPY --from=compiler /src/app/a.out /server

ENTRYPOINT [ "/server" ]