package models

import (
	"time"

	"gorm.io/gorm"
)

// MailNode represents a server node that can receive mail
type MailNode struct {
	ID        string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Name      string    `gorm:"uniqueIndex;not null;type:varchar(100)" json:"name"`
	Hostname  string    `gorm:"type:varchar(255)" json:"hostname"`
	IPAddress string    `gorm:"not null;type:varchar(45)" json:"ipAddress"`
	Region    string    `gorm:"type:varchar(50)" json:"region"`
	Status    string    `gorm:"type:varchar(20);default:'ACTIVE'" json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	Domains []Domain `gorm:"foreignKey:NodeID" json:"domains,omitempty"`
}

// Domain represents a routable domain (either system-wide or custom tenant).
type Domain struct {
	ID         string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID   *string   `gorm:"index" json:"tenantId"`
	NodeID     *string   `gorm:"index" json:"nodeId"`
	DomainName string    `gorm:"uniqueIndex;not null" json:"domainName"`
	IsPublic   bool      `gorm:"default:true" json:"isPublic"`
	Status     string    `gorm:"type:varchar(30);default:'PENDING'" json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`

	Node      *MailNode `gorm:"foreignKey:NodeID" json:"node,omitempty"`
	Mailboxes []Mailbox `gorm:"foreignKey:DomainID" json:"mailboxes,omitempty"`
}

// Mailbox is a specific receiving address.
type Mailbox struct {
	ID        string     `gorm:"primaryKey;type:varchar(36)" json:"id"`
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
	ID               string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
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
	ID          string `gorm:"primaryKey;type:varchar(36)" json:"id"`
	MessageID   string `gorm:"index" json:"messageId"`
	Filename    string `gorm:"type:varchar(255)" json:"filename"`
	ContentType string `gorm:"type:varchar(100)" json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	S3Key       string `gorm:"type:varchar(255)" json:"s3Key"` // Ref to Cloudflare R2
}

// DomainFilter represents a blocked or allowed sender domain
type DomainFilter struct {
	ID         string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Pattern    string    `gorm:"uniqueIndex;not null;type:varchar(255)" json:"pattern"` // e.g. "spam.com" or "*.spam.com"
	FilterType string    `gorm:"not null;type:varchar(10)" json:"filterType"`           // BLOCK or ALLOW
	Reason     string    `gorm:"type:text" json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}

// APIKey represents an external API key for frontend websites consuming this backend
type APIKey struct {
	ID          string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Name        string    `gorm:"not null;type:varchar(100)" json:"name"`
	KeyHash     string    `gorm:"uniqueIndex;not null;type:varchar(64)" json:"-"` // SHA-256 hash of the key
	KeyPrefix   string    `gorm:"type:varchar(8)" json:"keyPrefix"`               // First 8 chars for identification
	Permissions string    `gorm:"type:varchar(255);default:'read,write'" json:"permissions"`
	RateLimit   int       `gorm:"default:100" json:"rateLimit"`              // requests per minute
	IsInternal  bool      `gorm:"default:false" json:"isInternal"`            // bypass rate limiting for internal tools
	Status      string    `gorm:"type:varchar(20);default:'ACTIVE'" json:"status"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AuditLog for administrative actions
type AuditLog struct {
	ID        string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
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
		&MailNode{},
		&Domain{},
		&Mailbox{},
		&Message{},
		&Attachment{},
		&DomainFilter{},
		&APIKey{},
		&AuditLog{},
	)
}
