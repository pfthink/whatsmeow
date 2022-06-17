// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package sqlstore

import (
	"database/sql"
)

type upgradeFunc func(*sql.Tx, *Container) error

// Upgrades is a list of functions that will upgrade a database to the latest version.
//
// This may be of use if you want to manage the database fully manually, but in most cases you
// should just call Container.Upgrade to let the library handle everything.
var Upgrades = [...]upgradeFunc{upgradeV1, upgradeV2}

func (c *Container) getVersion() (int, error) {
	_, err := c.db.Exec("CREATE TABLE IF NOT EXISTS whatsmeow_version (version INTEGER)")
	if err != nil {
		return -1, err
	}

	version := 0
	row := c.db.QueryRow("SELECT version FROM whatsmeow_version LIMIT 1")
	if row != nil {
		_ = row.Scan(&version)
	}
	return version, nil
}

func (c *Container) setVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec("DELETE FROM whatsmeow_version")
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO whatsmeow_version (version) VALUES (?)", version)
	return err
}

// Upgrade upgrades the database from the current to the latest version available.
func (c *Container) Upgrade() error {
	version, err := c.getVersion()
	if err != nil {
		return err
	}

	for ; version < len(Upgrades); version++ {
		var tx *sql.Tx
		tx, err = c.db.Begin()
		if err != nil {
			return err
		}

		migrateFunc := Upgrades[version]
		c.log.Infof("Upgrading database to v%d", version+1)
		err = migrateFunc(tx, c)
		if err != nil {
			_ = tx.Rollback()
			return err
		}

		if err = c.setVersion(tx, version+1); err != nil {
			return err
		}

		if err = tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func upgradeV1(tx *sql.Tx, _ *Container) error {
	_, err := tx.Exec(`create table IF NOT EXISTS whatsmeow_device
(
    jid                varchar(100)
        primary key,
    registration_id    BIGINT  not null,
    noise_key          varchar(32)   not null,
    identity_key       varchar(32)   not null,
    signed_pre_key     varchar(32)   not null,
    signed_pre_key_id  int not null,
    signed_pre_key_sig varchar(64)   not null,
    adv_key            varchar(64)   not null,
    adv_details        varchar(64)   not null,
    adv_account_sig    varchar(64)   not null,
    adv_device_sig     varchar(64)   not null,
    platform           varchar(100) default '' not null,
    business_name      varchar(100) default '' not null,
    push_name          varchar(100) default '' not null
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_identity_keys
(
    our_jid   varchar(100) NOT NULL,
    their_id  varchar(100),
    identity  varchar(32) not null,
    unique key (our_jid, their_id)
)`)
	if err != nil {
		return err
	}
	/*_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_pre_keys
	(
	    jid     varchar(100),
	    key_id   int,
	    key      varchar(32)   not null,
	    uploaded int not null,
	    unique key (jid, key_id)
	   )`)
		if err != nil {
			return err
		}*/
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_sessions
(
    our_jid  varchar(100),
    their_id varchar(100),
    session  varchar(5000),
    unique key (our_jid, their_id)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_sender_keys
(
    our_jid    varchar(100),
    chat_id    varchar(100),
    sender_id  varchar(100),
    sender_key varchar(100) not null,
    unique key (our_jid, chat_id, sender_id)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_app_state_sync_keys
(
    jid        varchar(100),
    key_id      varchar(64),
    key_data    varchar(64)  not null,
    timestamp   datetime not null,
    fingerprint varchar(64)  not null,
    unique key (jid, key_id)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_app_state_version
(
    jid     varchar(100),
    name    varchar(100),
    version BIGINT not null,
    hash    varchar(500)  not null,
    unique key (jid, name)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_app_state_mutation_macs
(
    jid       varchar(100),
    name      varchar(100),
    version   BIGINT,
    index_mac varchar(500),
    value_mac varchar(500) not null,
    unique key (jid, name, version, index_mac)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_contacts
(
    our_jid      varchar(100),
    their_jid    varchar(100),
    first_name   varchar(100),
    full_name    varchar(100),
    push_name    varchar(100),
    business_name varchar(100),
    unique key (our_jid, their_jid)
)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`create table IF NOT EXISTS whatsmeow_chat_settings
(
    our_jid     varchar(100),
    chat_jid    varchar(100),
    muted_until BIGINT  default 0 not null,
    pinned      int default 0 not null,
    archived    int default 0 not null,
    unique key (our_jid, chat_jid)
)`)
	if err != nil {
		return err
	}
	return nil
}

const fillSigKeyPostgres = `
UPDATE whatsmeow_device SET adv_account_sig_key=(
	SELECT identity
	FROM whatsmeow_identity_keys
	WHERE our_jid=whatsmeow_device.jid
	  AND their_id=concat(split_part(whatsmeow_device.jid, '.', 1), ':0')
);
DELETE FROM whatsmeow_device WHERE adv_account_sig_key IS NULL;
ALTER TABLE whatsmeow_device ALTER COLUMN adv_account_sig_key SET NOT NULL;
`

const fillSigKeySQLite = `
UPDATE whatsmeow_device SET adv_account_sig_key=(
	SELECT identity
	FROM whatsmeow_identity_keys
	WHERE our_jid=whatsmeow_device.jid
	  AND their_id=substr(whatsmeow_device.jid, 0, instr(whatsmeow_device.jid, '.')) || ':0'
)
`

func upgradeV2(tx *sql.Tx, container *Container) error {
	/*_, err := tx.Exec("ALTER TABLE whatsmeow_device ADD COLUMN adv_account_sig_key bytea CHECK ( length(adv_account_sig_key) = 32 )")
	if err != nil {
		return err
	}
	if container.dialect == "postgres" {
		_, err = tx.Exec(fillSigKeyPostgres)
	} else if container.dialect == "sqlite3" {
		_, err = tx.Exec(fillSigKeySQLite)
	} else {
		fmt.Println("mysql not exec")
	}

	return err*/
	return nil
}
