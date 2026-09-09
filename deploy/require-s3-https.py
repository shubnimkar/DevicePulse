#!/usr/bin/env python3
"""Preserve existing grants and add a non-TLS deny. AWS CLI credentials required."""
import argparse
import copy
import json
import subprocess
import tempfile
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bucket', required=True)
parser.add_argument('--region', required=True)
parser.add_argument('--apply', action='store_true')
args = parser.parse_args()

def aws(operation, *extra):
    result = subprocess.run(['aws', 's3api', operation, '--bucket', args.bucket,
                             '--region', args.region, '--no-cli-pager', '--output', 'json', *extra],
                            capture_output=True, text=True, check=True)
    return json.loads(result.stdout) if result.stdout.strip() else None

original = json.loads(aws('get-bucket-policy')['Policy'])
statement = {
    'Sid': 'DevicePulseDenyInsecureTransport', 'Effect': 'Deny', 'Principal': '*',
    'Action': 's3:*',
    'Resource': [f'arn:aws:s3:::{args.bucket}', f'arn:aws:s3:::{args.bucket}/*'],
    'Condition': {'Bool': {'aws:SecureTransport': 'false', 'aws:PrincipalIsAWSService': 'false'}}
}
updated = copy.deepcopy(original)
statements = updated.setdefault('Statement', [])
if not isinstance(statements, list):
    raise SystemExit('Unexpected policy format; refusing to modify')
matching = [s for s in statements if s.get('Sid') == statement['Sid']]
if matching:
    if matching == [statement]:
        print('HTTPS enforcement already present; no change needed.')
        raise SystemExit(0)
    raise SystemExit('Statement ID already exists with different contents; refusing to overwrite')
statements.append(statement)
print('Add this statement; preserve all existing statements:')
print(json.dumps(statement, indent=2))
if not args.apply:
    print('Preview only. Pass --apply to save a backup, apply, and verify.')
    raise SystemExit(0)
backup_dir = Path(tempfile.mkdtemp(prefix='devicepulse-s3-policy-'))
backup = backup_dir / 'before.json'
backup.write_text(json.dumps(original, indent=2))
backup.chmod(0o600)
policy_file = backup_dir / 'after.json'
policy_file.write_text(json.dumps(updated, indent=2))
policy_file.chmod(0o600)
if json.loads(aws('get-bucket-policy')['Policy']) != original:
    raise SystemExit('Policy changed during preparation; refusing to overwrite')
aws('put-bucket-policy', '--policy', 'file://' + str(policy_file))
actual = json.loads(aws('get-bucket-policy')['Policy'])
if actual != updated:
    raise SystemExit(f'Policy verification differs; inspect manually. Backup: {backup}')
print(f'Applied and verified. Original policy backup: {backup}')
