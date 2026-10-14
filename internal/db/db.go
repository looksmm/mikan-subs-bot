// Code copied from mikan (db) by tools/extract: the declarations the bot needs.

package db

import (
	"database/sql"
)

type ListNoticeSubscriptionsRow struct {
	User       User
	TgID       int64
	GrantsLeft int64
}

type BoundDevice struct {
	ID        int64
	UserID    int64
	Hwid      string
	SlotID    int64
	Os        string
	OsVersion string
	Model     string
	App       string
	LastIp    string
	CreatedAt int64
	LastSeen  int64
	Name      string
}

type Payment struct {
	ID         int64
	Provider   string
	Payload    string
	ExternalID sql.NullString
	TgID       int64
	Kind       string
	UserID     sql.NullInt64
	TariffID   sql.NullInt64
	PackageID  sql.NullInt64
	TariffName string
	Amount     int64
	Currency   string
	Status     string
	Error      string
	PayUrl     string
	CreatedAt  int64
	PaidAt     sql.NullInt64
	AppliedAt  sql.NullInt64
	RefundedAt sql.NullInt64
	TermDays   sql.NullInt64
	Revert     string
}

type Tariff struct {
	ID            int64
	Name          string
	TrafficLimit  sql.NullInt64
	DurationDays  int64
	DeviceLimit   sql.NullInt64
	ResetStrategy string
	PriceLabel    string
	Sort          int64
	Archived      int64
	CreatedAt     int64
	BillingDay    sql.NullInt64
	PriceStars    sql.NullInt64
	PriceRub      sql.NullInt64
	OnSale        int64
	SpeedLimit    sql.NullInt64
	Description   string
	DeviceMode    string
}

type TgChat struct {
	TgID      int64
	Username  string
	FirstName string
	MenuMsgID int64
	Current   int64
	Blocked   int64
	CreatedAt int64
	UpdatedAt int64
}

type TrafficPackage struct {
	ID         int64
	Name       string
	Bytes      int64
	PoolID     sql.NullInt64
	Lifetime   string
	Days       int64
	PriceStars sql.NullInt64
	PriceRub   sql.NullInt64
	OnSale     int64
	Sort       int64
	Archived   int64
	CreatedAt  int64
}

type User struct {
	ID            int64
	Name          string
	Contact       string
	Note          string
	Tags          string
	Status        string
	TariffID      sql.NullInt64
	TrafficLimit  sql.NullInt64
	DeviceLimit   sql.NullInt64
	ResetStrategy string
	PeriodDays    int64
	PeriodStart   int64
	UsedUp        int64
	UsedDown      int64
	TotalUp       int64
	TotalDown     int64
	ExpiresAt     sql.NullInt64
	Inbounds      sql.NullString
	SubToken      string
	SlotID        sql.NullInt64
	OnlineAt      sql.NullInt64
	CreatedAt     int64
	UpdatedAt     int64
	BillingDay    sql.NullInt64
	Source        string
	Hidden        int64
	FolderID      sql.NullInt64
	SpeedLimit    sql.NullInt64
	DeviceMode    string
}
