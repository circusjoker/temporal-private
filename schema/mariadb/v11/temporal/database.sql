-- COLLATE is explicit because MariaDB's default utf8mb4 collation is PAD SPACE
-- (utf8mb4_uca1400_ai_ci) while MySQL 8's is NO PAD, which would make values
-- differing only by trailing spaces collide. See sqlplugin/mariadb/plugin.go.
CREATE DATABASE temporal CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_nopad_ai_ci;
