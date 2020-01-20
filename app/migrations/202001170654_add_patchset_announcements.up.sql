-- noinspection SqlNoDataSourceInspectionForFile

CREATE TABLE patchset_announcements (
       num INTEGER NOT NULL,
       project_name TEXT NOT NULL,
       change_num INTEGER NOT NULL,
       patchset_num INTEGER NOT NULL,
       message_handle TEXT NOT NULL,
       ts TIMESTAMP NOT NULL,
       PRIMARY KEY ( num )
);

CREATE INDEX patchset_announcement_project_name_change_num_patchset_num_idx ON patchset_announcements ( project_name, change_num, patchset_num );
CREATE INDEX patchset_announcement_ts_idx ON patchset_announcements ( ts );
