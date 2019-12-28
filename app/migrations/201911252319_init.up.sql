CREATE TABLE gerrit_users (
	gerrit_username text NOT NULL,
	chat_id text NOT NULL,
	last_report timestamp,
	PRIMARY KEY ( gerrit_username )
);

CREATE INDEX last_report_idx ON gerrit_users ( last_report );
