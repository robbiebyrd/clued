-- MySQL dump of the shop database: backticks, AUTO_INCREMENT and table
-- options this grammar does not know.
USE `shop`;

CREATE TABLE `customers` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(100) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE `orders` (
  `id` int NOT NULL AUTO_INCREMENT,
  `customer_id` int NOT NULL REFERENCES `customers` (`id`),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;

CREATE INDEX `orders_customer_idx` ON `orders` (`customer_id`);

INSERT INTO `customers` (`name`) VALUES ('Ada');

SELECT o.id, c.name FROM `orders` o JOIN `customers` c ON c.id = o.customer_id;
