package models

import (
	"time"

	"gorm.io/gorm"
)

// User represents a user in the web application plane.
type User struct {
	ID           string    `gorm:"primaryKey;type:varchar(30)" json:"id"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Status       string    `gorm:"type:varchar(20);default:'ACTIVE'" json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`

	Roles         []Role         `gorm:"many2many:user_roles;" json:"roles"`
	Subscriptions []Subscription `json:"subscriptions"`
}

// Role represents an RBAC role (e.g., SUPER_ADMIN, USER_FREE)
type Role struct {
	ID          string       `gorm:"primaryKey;type:varchar(30)" json:"id"`
	Name        string       `gorm:"uniqueIndex;not null" json:"name"`
	Description string       `json:"description"`
	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions"`
}

// Permission is a granular action (e.g. "mailbox:create")
type Permission struct {
	ID          string `gorm:"primaryKey;type:varchar(30)" json:"id"`
	Name        string `gorm:"uniqueIndex;not null" json:"name"`
	Description string `json:"description"`
}

// Plan represents a billing logic constraint set.
type Plan struct {
	ID                string `gorm:"primaryKey;type:varchar(30)" json:"id"`
	Name              string `gorm:"uniqueIndex;not null" json:"name"`
	MaxMailboxes      int    `gorm:"default:1" json:"maxMailboxes"`
	CustomDomainLimit int    `gorm:"default:0" json:"customDomainLimit"`
	RetentionDays     int    `gorm:"default:1" json:"retentionDays"`
}

// Subscription links a user to a plan.
type Subscription struct {
	ID        string    `gorm:"primaryKey;type:varchar(30)" json:"id"`
	UserID    string    `gorm:"index;not null" json:"userId"`
	PlanID    string    `gorm:"index;not null" json:"planId"`
	Status    string    `gorm:"type:varchar(20);default:'ACTIVE'" json:"status"`
	StartDate time.Time `gorm:"autoCreateTime" json:"startDate"`
	EndDate   time.Time `json:"endDate"`

	Plan Plan `gorm:"foreignKey:PlanID" json:"plan"`
}

// Domain represents a routable domain (either system-wide or custom tenant).
type Domain struct {
	ID         string    `gorm:"primaryKey;type:varchar(30)" json:"id"`
	TenantID   *string   `gorm:"index" json:"tenantId"` // Nullable for public tempmail domains
	DomainName string    `gorm:"uniqueIndex;not null" json:"domainName"`
	Status     string    `gorm:"type:varchar(30);default:'PENDING'" json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`

	Mailboxes []Mailbox `gorm:"foreignKey:DomainID" json:"mailboxes,omitempty"`
}

// Mailbox is a specific receiving address.
type Mailbox struct {
	ID        string     `gorm:"primaryKey;type:varchar(30)" json:"id"`
	LocalPart string     `gorm:"index:idx_routing,unique;not null" json:"localPart"`
	DomainID  string     `gorm:"index:idx_routing,unique;not null" json:"domainId"`
	TenantID  string     `gorm:"index;not null" json:"tenantId"`
	Status    string     `gorm:"type:varchar(20);default:'ACTIVE'" json:"status"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`

	Domain   Domain    `gorm:"foreignKey:DomainID" json:"domain"`
	Messages []Message `gorm:"foreignKey:MailboxID" json:"messages,omitempty"`
}

// Message is an ingested email
type Message struct {
	ID               string    `gorm:"primaryKey;type:varchar(30)" json:"id"`
	MailboxID        string    `gorm:"index" json:"mailboxId"`
	FromAddress      string    `gorm:"type:varchar(255)" json:"fromAddress"`
	ToAddress        string    `gorm:"type:varchar(255)" json:"toAddress"`
	Subject          string    `gorm:"type:text" json:"subject"`
	TextBody         string    `gorm:"type:text" json:"textBody"`
	HTMLBody         string    `gorm:"type:text" json:"htmlBody"`
	S3KeyRaw         string    `gorm:"index;type:varchar(255)" json:"s3KeyRaw"` // Ref to Cloudflare R2
	SpamScore        float64   `gorm:"default:0.0" json:"spamScore"`
	QuarantineAction string    `gorm:"type:varchar(20);default:'ACCEPT'" json:"quarantineAction"`
	ExpiresAt        time.Time `gorm:"index" json:"expiresAt"`
	ReceivedAt       time.Time `gorm:"autoCreateTime" json:"receivedAt"`

	Attachments []Attachment `gorm:"foreignKey:MessageID" json:"attachments,omitempty"`
}

// Attachment metadata
type Attachment struct {
	ID          string `gorm:"primaryKey;type:varchar(30)" json:"id"`
	MessageID   string `gorm:"index" json:"messageId"`
	Filename    string `gorm:"type:varchar(255)" json:"filename"`
	ContentType string `gorm:"type:varchar(100)" json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	S3Key       string `gorm:"type:varchar(255)" json:"s3Key"` // Ref to Cloudflare R2
}

// AuditLog for administrative actions
type AuditLog struct {
	ID        string    `gorm:"primaryKey;type:varchar(30)" json:"id"`
	UserID    *string   `gorm:"index" json:"userId"`
	Action    string    `gorm:"type:varchar(100)" json:"action"`
	TargetID  string    `gorm:"type:varchar(100)" json:"targetId"`
	Reason    string    `gorm:"type:text" json:"reason"`
	IPAddress string    `gorm:"type:varchar(45)" json:"ipAddress"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
}

// Migrate is a utility function to sync schema
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&User{},
		&Role{},
		&Permission{},
		&Plan{},
		&Subscription{},
		&Domain{},
		&Mailbox{},
		&Message{},
		&Attachment{},
		&AuditLog{},
	)
}
