/**
 * Inventory files with an extracted Go AST, rather than diagnostic-only locations.
 * This query supplies extraction evidence; it is not a security query.
 */
import go

from GoFile file
where file.getRelativePath().matches("apps/server/%")
select file.getRelativePath()
