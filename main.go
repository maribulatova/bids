package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func main() {
	db, err := sqlx.Connect("postgres", "postgres://postgres:postgres@postgres/bids?sslmode=disable")
	if err != nil {
		log.Fatalf("Obtain database connection: %v", err)
	}

	cancellingIntervalSec := 60
	cancelLimit := 10

	go func(intervalSec, limit int) {
		t := time.NewTicker(time.Second * time.Duration(intervalSec))
		for range t.C {
			if err := cancelTransactions(db, limit); err != nil {
				log.Printf("cancel transactions: %v", err)
			}
		}
	}(cancellingIntervalSec, cancelLimit)

	r := chi.NewRouter()
	r.Post("/transactions", transactionsHandler(db))

	log.Println("app started")
	log.Println(http.ListenAndServe("0.0.0.0:8080", r))
}

type TransactionJSON struct {
	TransactionID string `json:"transactionId"`
	State         string `json:"state"`
	Amount        string `json:"amount"`
}

type Player struct {
	ID          int    `db:"id"`
	PhoneNumber string `db:"phone_number"`
	Balance     int    `db:"balance"`
}

const phoneNumber = "1"

const (
	win  = "win"
	lose = "lose"
)

func transactionsHandler(db *sqlx.DB) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req TransactionJSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			internal(rw, err)
			return
		}
		sourceType := r.Header.Get("Source-Type")
		amount, err := parseAmount(req.Amount)
		if err != nil {
			bad(rw, err)
			return
		}

		tx, err := db.BeginTxx(r.Context(), &sql.TxOptions{})
		if err != nil {
			internal(rw, err)
			return
		}
		defer func() {
			_ = tx.Rollback()
		}()

		var player Player
		if err := tx.Get(&player, "select * from player where phone_number = $1 for update", phoneNumber); err != nil {
			internal(rw, err)
			return
		}

		_, err = tx.Exec("insert into transaction (transaction_id, player_id, state, amount, source) values ($1, $2, $3, $4, $5)",
			req.TransactionID, player.ID, req.State, amount, sourceType)
		if err != nil {
			// the transaction is already processed
			if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
				rw.WriteHeader(http.StatusOK)
				return
			}
			internal(rw, err)
			return
		}

		switch req.State {
		case win:
		case lose:
			if player.Balance < amount {
				bad(rw, fmt.Errorf("insufficient funds"))
				return
			}
			amount = -amount
		default:
			bad(rw, fmt.Errorf("state %s is not supported", req.State))
			return
		}

		if _, err := tx.Exec("update player set balance = $1 where phone_number = $2",
			player.Balance+amount, phoneNumber); err != nil {
			internal(rw, err)
			return
		}

		if err := tx.Commit(); err != nil {
			internal(rw, err)
			return
		}

		rw.WriteHeader(http.StatusOK)
	}
}

func cancelTransactions(db *sqlx.DB, limit int) error {
	tx, err := db.BeginTxx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	player := Player{}
	if err := tx.Get(&player, "select * from player where phone_number = $1 for update", phoneNumber); err != nil {
		return err
	}

	var transactions []struct {
		ID        string `db:"transaction_id"`
		State     string `db:"state"`
		Amount    int    `db:"amount"`
		Cancelled bool   `db:"cancelled"`
	}

	if err := tx.Select(&transactions, `
		select  transaction_id, state, amount, cancelled
		from transaction 
		order by created_at desc 
		limit $1`, limit,
	); err != nil {
		return err
	}

	delta := 0
	for _, t := range transactions {
		if !t.Cancelled {
			if t.State == win {
				delta -= t.Amount
			} else {
				delta += t.Amount
			}
			if _, err := tx.Exec("update transaction set cancelled = true where transaction_id = $1", t.ID); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec("update player set balance = $1 where id = $2", player.Balance+delta, player.ID); err != nil {
		return err
	}

	return tx.Commit()
}

func internal(rw http.ResponseWriter, err error) {
	rw.WriteHeader(http.StatusInternalServerError)
	if err != nil {
		if _, err := rw.Write([]byte(err.Error())); err != nil {
			log.Printf("Write response: %v\n", err)
		}
	}
}

func bad(rw http.ResponseWriter, err error) {
	rw.WriteHeader(http.StatusBadRequest)
	if err != nil {
		if _, err := rw.Write([]byte(err.Error())); err != nil {
			log.Printf("Write response: %v", err)
		}
	}
}

func parseAmount(amount string) (int, error) {
	var result int64
	amountFloat, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return 0, err
	}

	s := fmt.Sprintf("%.0f", amountFloat*1000)

	result, err = strconv.ParseInt(s, 0, 64)
	return int(result), err
}
