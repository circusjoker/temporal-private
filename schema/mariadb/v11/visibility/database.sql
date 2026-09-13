-- COLLATE is explicit because MariaDB's default utf8mb4 collation is PAD SPACE
-- (utf8mb4_uca1400_ai_ci) while MySQL 8's is NO PAD. See sqlplugin/mysql/admin.go.
CREATE DATABASE temporal CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_nopad_ai_ci;
CREATE DATABASE temporal_visibility CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_nopad_ai_ci;
