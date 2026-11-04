package mensageria

import (
	"log"
	"os"
)

// registro é para onde vão as mensagens desta camada: conexão, publicação,
// recebimento e descarte de evento inválido.
var registro = log.New(os.Stderr, "", log.Ltime)
