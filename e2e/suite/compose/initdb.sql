-- One database per deployment (the e2e spec, Topology).
CREATE DATABASE kycal_x;
CREATE DATABASE kycal_y;
CREATE DATABASE kyidentity ENCODING 'UTF8' LOCALE 'C' TEMPLATE template0; -- KyIdentity compares names as SQLite does
CREATE DATABASE kymessages;
