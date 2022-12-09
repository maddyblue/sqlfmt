FROM golang:1.19
ADD sqlfmt /
CMD ["/sqlfmt"]
